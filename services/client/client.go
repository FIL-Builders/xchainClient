package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"

	"github.com/FIL-Builders/xchainClient/config"
	"github.com/FIL-Builders/xchainClient/utils"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-libipfs/blocks"
	"github.com/ipfs/go-unixfsnode/data/builder"
	"github.com/ipld/go-car/v2"
	"github.com/ipld/go-car/v2/blockstore"
	dagpb "github.com/ipld/go-codec-dagpb"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/multiformats/go-multicodec"
	"github.com/multiformats/go-multihash"
	"github.com/urfave/cli/v2"

	commcid "github.com/filecoin-project/go-fil-commcid"
	commp "github.com/filecoin-project/go-fil-commp-hashhash"
)

func OfferFileAction(cctx *cli.Context) error {
	// Expect exactly 3 arguments: <file_path> <payment-addr> <payment-amount>
	if cctx.Args().Len() != 3 {
		return fmt.Errorf("Usage: <file_path> <payment-addr> <payment-amount>")
	}
	filePath := cctx.Args().Get(0)
	paymentAddr := cctx.Args().Get(1)
	paymentAmount := cctx.Args().Get(2)

	// Create the CAR file name.
	carFilePath := filePath + ".car"
	// Remove any existing CAR file.
	if _, err := os.Stat(carFilePath); err == nil {
		if err := os.Remove(carFilePath); err != nil {
			return fmt.Errorf("failed to remove existing CAR file: %v", err)
		}
	}

	// Create the CAR file (using our stub; replace with proper go-car logic as needed).
	if err := createCarFile(filePath, carFilePath); err != nil {
		return fmt.Errorf("failed to create CAR file: %v", err)
	}

	// Compute the CommP and padded piece size.
	commPStr, paddedSize, err := calcStreamCommp(carFilePath)
	if err != nil {
		return fmt.Errorf("failed to compute CommP: %v", err)
	}
	sizeStr := strconv.FormatUint(paddedSize, 10)

	// Data Plane: Upload the CAR file to the local server.
	carFileBytes, err := os.ReadFile(carFilePath)
	if err != nil {
		return fmt.Errorf("failed to read CAR file: %v", err)
	}
	urlPut := "http://localhost:5077/put"
	resp, err := http.Post(urlPut, "application/octet-stream", bytes.NewReader(carFileBytes))
	if err != nil {
		return fmt.Errorf("failed to POST CAR file: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %v", err)
	}
	var postResult map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &postResult); err != nil {
		return fmt.Errorf("failed to parse JSON response: %v", err)
	}
	var bufferID string
	if idStr, ok := postResult["id"].(string); ok {
		bufferID = idStr
	} else if idNum, ok := postResult["id"].(float64); ok {
		bufferID = strconv.FormatFloat(idNum, 'f', -1, 64)
	} else {
		return fmt.Errorf("failed to extract buffer id from response")
	}
	bufferAddr := fmt.Sprintf("http://localhost:5077/get?id=%s", bufferID)

	// Blockchain: Load configuration and prepare the transaction.
	cfg, err := config.LoadConfig(cctx.String("config"))
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}
	chainName := cctx.String("chain")
	srcCfg, err := config.GetSourceConfig(cfg, chainName)
	if err != nil {
		return fmt.Errorf("invalid chain name '%s': %v", chainName, err)
	}
	client, err := ethclient.Dial(srcCfg.Api)
	if err != nil {
		return fmt.Errorf("failed to connect to Ethereum client for source chain %s at %s: %v", chainName, srcCfg.Api, err)
	}
	contractAddress := common.HexToAddress(srcCfg.OnRampAddress)
	parsedABI, err := utils.LoadAbi(cfg.OnRampABIPath)
	if err != nil {
		return fmt.Errorf("failed to load ABI: %v", err)
	}
	onramp := bind.NewBoundContract(contractAddress, *parsedABI, client, client, client)
	auth, err := utils.LoadPrivateKey(cfg, srcCfg.ChainID)
	if err != nil {
		return fmt.Errorf("failed to load private key: %v", err)
	}

	// Map parameters for the offer.
	// Here we use the computed commPStr both as the CommP and (as a placeholder) as the file CID.
	offerObj, err := MakeOffer(commPStr, sizeStr, commPStr, bufferAddr, paymentAddr, paymentAmount, *parsedABI)
	if err != nil {
		return fmt.Errorf("failed to pack offer data params: %v", err)
	}

	// Submit the offer transaction.
	tx, err := onramp.Transact(auth, "offerData", offerObj)
	if err != nil {
		return fmt.Errorf("failed to send transaction: %v", err)
	}
	log.Printf("Waiting for transaction: %s\n", tx.Hash().Hex())
	receipt, err := bind.WaitMined(cctx.Context, client, tx)
	if err != nil {
		return fmt.Errorf("failed to wait for tx: %v", err)
	}
	log.Printf("Tx %s included: %d", tx.Hash().Hex(), receipt.Status)
	return nil
}

func OfferCarAction(cctx *cli.Context) error {
	cfg, err := config.LoadConfig(cctx.String("config"))
	if err != nil {
		log.Fatal(err)
	}

	// Get chain name
	chainName := cctx.String("chain")
	srcCfg, err := config.GetSourceConfig(cfg, chainName)
	if err != nil {
		log.Fatalf("Invalid chain name '%s': %v", chainName, err)
	}

	// Dial network
	client, err := ethclient.Dial(srcCfg.Api)
	if err != nil {
		log.Fatalf("failed to connect to Ethereum client for source chain %s at %s: %v", chainName, srcCfg.Api, err)
	}

	// Load onramp contract handle
	contractAddress := common.HexToAddress(srcCfg.OnRampAddress)
	parsedABI, err := utils.LoadAbi(cfg.OnRampABIPath)
	if err != nil {
		log.Fatal(err)
	}
	onramp := bind.NewBoundContract(contractAddress, *parsedABI, client, client, client)
	if err != nil {
		log.Fatal(err)
	}

	// Get auth
	auth, err := utils.LoadPrivateKey(cfg, srcCfg.ChainID)
	if err != nil {
		log.Fatal(err)
	}

	// Send Tx
	offer, err := MakeOffer(
		cctx.Args().First(),
		cctx.Args().Get(1),
		cctx.Args().Get(2),
		cctx.Args().Get(3),
		cctx.Args().Get(4),
		cctx.Args().Get(5),
		*parsedABI,
	)

	if err != nil {
		log.Fatalf("failed to pack offer data params: %v", err)
	}
	tx, err := onramp.Transact(auth, "offerData", offer)
	if err != nil {
		log.Fatalf("failed to send tx: %v", err)
	}

	log.Printf("Waiting for transaction: %s\n", tx.Hash().Hex())
	receipt, err := bind.WaitMined(cctx.Context, client, tx)
	if err != nil {
		log.Fatalf("failed to wait for tx: %v", err)
	}
	log.Printf("Tx %s included: %d", tx.Hash().Hex(), receipt.Status)

	return nil
}

func createCarFile(inputPath, outputPath string) error {
	ctx := context.Background()

	// Create a placeholder CID for initialization
	hasher, err := multihash.GetHasher(multihash.SHA2_256)
	if err != nil {
		return err
	}
	digest := hasher.Sum([]byte{})
	hash, err := multihash.Encode(digest, multihash.SHA2_256)
	if err != nil {
		return err
	}
	proxyRoot := cid.NewCidV1(uint64(multicodec.DagPb), hash)

	// Open CAR file for writing
	options := []car.Option{blockstore.WriteAsCarV1(true)}

	// Open CAR file for writing
	bs, err := blockstore.OpenReadWrite(outputPath, []cid.Cid{proxyRoot}, options...)

	if err != nil {
		return err
	}
	defer bs.Finalize()

	// Write UnixFS DAG
	rootCID, err := writeFileToCar(ctx, bs, inputPath)
	if err != nil {
		return err
	}
	fmt.Println("CID ", rootCID)

	// Replace root with actual rootCID
	return car.ReplaceRootsInFile(outputPath, []cid.Cid{rootCID})
}

func writeFileToCar(ctx context.Context, bs *blockstore.ReadWrite, filePath string) (cid.Cid, error) {
	ls := cidlink.DefaultLinkSystem()
	ls.TrustedStorage = true

	ls.StorageReadOpener = func(_ ipld.LinkContext, l ipld.Link) (io.Reader, error) {
		cl, ok := l.(cidlink.Link)
		if !ok {
			return nil, fmt.Errorf("not a cidlink")
		}
		blk, err := bs.Get(ctx, cl.Cid)
		if err != nil {
			return nil, err
		}
		return bytes.NewBuffer(blk.RawData()), nil
	}

	ls.StorageWriteOpener = func(_ ipld.LinkContext) (io.Writer, ipld.BlockWriteCommitter, error) {
		buf := bytes.NewBuffer(nil)
		return buf, func(l ipld.Link) error {
			cl, ok := l.(cidlink.Link)
			if !ok {
				return fmt.Errorf("not a cidlink")
			}
			blk, err := blocks.NewBlockWithCid(buf.Bytes(), cl.Cid)
			if err != nil {
				return err
			}
			return bs.Put(ctx, blk)
		}, nil
	}

	// Open file and get its actual size
	fi, err := os.Stat(filePath)
	if err != nil {
		return cid.Undef, err
	}
	fileSize := fi.Size()

	// Build UnixFS DAG for input file
	l, _, err := builder.BuildUnixFSRecursive(filePath, &ls)
	if err != nil {
		return cid.Undef, err
	}

	// Ensure size is set correctly
	entry, err := builder.BuildUnixFSDirectoryEntry(path.Base(filePath), fileSize, l)

	if err != nil {
		return cid.Undef, err
	}

	root, _, err := builder.BuildUnixFSDirectory([]dagpb.PBLink{entry}, &ls)
	if err != nil {
		return cid.Undef, err
	}

	rcl, ok := root.(cidlink.Link)
	if !ok {
		return cid.Undef, fmt.Errorf("could not interpret root as CID link")
	}

	fmt.Printf("Generated CAR Root CID: %s with File Size: %d\n", rcl.Cid.String(), fileSize)

	return rcl.Cid, nil
}

const BufSize = ((16 << 20) / 128 * 127)

// calcStreamCommp computes the CommP and padded piece size for a given CAR file.
func calcStreamCommp(carPath string) (string, uint64, error) {
	f, err := os.Open(carPath)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	// Create a new CommP calculator
	cp := new(commp.Calc)

	// Buffered reader to optimize IO performance
	streamBuf := bufio.NewReaderSize(f, BufSize)

	// Feed data into commp.Calc via io.TeeReader
	_, err = io.Copy(cp, streamBuf)
	if err != nil {
		return "", 0, err
	}

	// Compute raw CommP and padded size
	rawCommP, paddedSize, err := cp.Digest()
	if err != nil {
		return "", 0, err
	}

	// Convert raw CommP to CID
	commCid, err := commcid.DataCommitmentV1ToCID(rawCommP)
	if err != nil {
		return "", 0, err
	}

	return commCid.String(), paddedSize, nil
}

func MakeOffer(commpStr string, sizeStr string, cidStr string, location string, token string, amountStr string, abi abi.ABI) (*Offer, error) {
	log.Printf("MakeOffer called with commpStr: %s, sizeStr: %s, cidStr: %s,  location: %s, token: %s, amountStr: %s\n", commpStr, sizeStr, cidStr, location, token, amountStr)

	commP, err := cid.Decode(commpStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cid %w", err)
	}

	size, err := strconv.Atoi(sizeStr)
	if err != nil {
		return nil, err
	}
	amount, err := strconv.Atoi(amountStr)
	if err != nil {
		return nil, err
	}

	amountBig := big.NewInt(0).SetUint64(uint64(amount))

	offer := Offer{
		CommP:    commP.Bytes(),
		Location: location,
		Cid:      cidStr,
		Token:    common.HexToAddress(token),
		Amount:   amountBig,
		Size:     uint64(size),
	}

	return &offer, nil
}

// Mirror OnRamp.sol's `Offer` struct
type Offer struct {
	CommP    []uint8        `json:"commP"`
	Size     uint64         `json:"size"`
	Cid      string         `json:"cid"`
	Location string         `json:"location"`
	Amount   *big.Int       `json:"amount"`
	Token    common.Address `json:"token"`
}

// generateEthereumAccount creates a new Ethereum account and saves it to the specified JSON file
func GenerateEthereumAccount(keystoreFile, password string) (string, error) {
	// Validate file extension
	if filepath.Ext(keystoreFile) != ".json" {
		return "", fmt.Errorf("keystore file must have a .json extension")
	}

	// Ensure directory exists
	keystoreDir := filepath.Dir(keystoreFile)
	if err := os.MkdirAll(keystoreDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create keystore directory: %v", err)
	}

	// Create a temporary keystore
	tempDir, err := os.MkdirTemp("", "keystore-tmp")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary keystore directory: %v", err)
	}
	defer os.RemoveAll(tempDir) // Cleanup temp keystore

	ks := keystore.NewKeyStore(tempDir, keystore.StandardScryptN, keystore.StandardScryptP)

	// Generate a new account
	account, err := ks.NewAccount(password)
	if err != nil {
		return "", fmt.Errorf("failed to create new account: %v", err)
	}

	// Read the generated keystore file
	keyJSON, err := os.ReadFile(account.URL.Path)
	if err != nil {
		return "", fmt.Errorf("failed to read generated keystore file: %v", err)
	}

	// Save to the specified file path
	if err := os.WriteFile(keystoreFile, keyJSON, 0600); err != nil {
		return "", fmt.Errorf("failed to save keystore file: %v", err)
	}

	return account.Address.Hex(), nil
}
