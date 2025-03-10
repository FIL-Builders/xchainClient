package aggregator

import (
	"os"
	"path/filepath"

	"github.com/FIL-Builders/xchainClient/config"
	"github.com/FIL-Builders/xchainClient/services/buffer"
	"github.com/FIL-Builders/xchainClient/utils"

	"bytes"
	"context"
	"encoding/hex"

	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"math/bits"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	boosttypes "github.com/filecoin-project/boost/storagemarket/types"
	boosttypes2 "github.com/filecoin-project/boost/transport/types"
	"github.com/filecoin-project/go-address"
	cborutil "github.com/filecoin-project/go-cbor-util"
	"github.com/filecoin-project/go-data-segment/datasegment"
	"github.com/filecoin-project/go-data-segment/merkletree"
	"github.com/filecoin-project/go-jsonrpc"
	inet "github.com/libp2p/go-libp2p/core/network"

	filabi "github.com/filecoin-project/go-state-types/abi"
	fbig "github.com/filecoin-project/go-state-types/big"
	builtintypes "github.com/filecoin-project/go-state-types/builtin"
	"github.com/filecoin-project/go-state-types/builtin/v9/market"
	"github.com/filecoin-project/go-state-types/crypto"
	"github.com/filecoin-project/lotus/api/v0api"
	lotustypes "github.com/filecoin-project/lotus/chain/types"
	"github.com/google/uuid"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const (
	// PODSI aggregation uses 64 extra bytes per piece
	// pieceOverhead = uint64(64) TODO uncomment this when we are smarter about determining threshold crossing
	// Piece CID of small valid car (below) that must be prepended to the aggregation for deal acceptance
	prefixCARCid = "baga6ea4seaqiklhpuei4wz7x3wwpvnul3sscfyrz2dpi722vgpwlolfky2dmwey"
	// Hex of the prefix car file
	prefixCAR = "3aa265726f6f747381d82a58250001701220b9ecb605f194801ee8a8355014e7e6e62966f94ccb6081" +
		"631e82217872209dae6776657273696f6e014101551220704a26a32a76cf3ab66ffe41eb27adefefe9c93206960bb0" +
		"147b9ed5e1e948b0576861744966487567684576657265747449494957617352696768743f5601701220b9ecb605f1" +
		"94801ee8a8355014e7e6e62966f94ccb6081631e82217872209dae122c0a2401551220704a26a32a76cf3ab66ffe41" +
		"eb27adefefe9c93206960bb0147b9ed5e1e948b012026576181d0a020801"
	// Size of the padded prefix car in bytes
	prefixCARSizePadded = uint64(256)
	// libp2p identifier for latest deal protocol
	DealProtocolv120 = "/fil/storage/mk/1.2.0"
	// Delay to start deal at. For 2k devnet 4 second block time this is 13.3 minutes TODO Config
	dealDelayEpochs = 3000
	// Storage deal duration, TODO figure out what to do about this, either comes from offer or config
	dealDuration = 518400 // 6 months (on mainnet)
)

type aggregator struct {
	client           *ethclient.Client         // raw client for log subscriptions
	onramp           *bind.BoundContract       // onramp binding over raw client for message sending
	auth             *bind.TransactOpts        // auth for message sending
	abi              *abi.ABI                  // onramp abi for log subscription and message sending
	onrampAddr       common.Address            // onramp address for log subscription
	proverAddr       common.Address            // prover address for client contract deal
	payoutAddr       common.Address            // aggregator payout address for receiving funds
	ch               chan DataReadyEvent       // pass events to seperate goroutine for processing
	transfers        map[int]AggregateTransfer // track aggregate data awaiting transfer
	transferLk       sync.RWMutex              // Mutex protecting transfers map
	transferID       int                       // ID of the next transfer
	transferAddr     string                    // address to listen for transfer requests
	minDealSize      uint64                    // minimum deal size
	targetDealSize   uint64                    // how big aggregates should be
	host             host.Host                 // libp2p host for deal protocol to boost
	spDealAddr       *peer.AddrInfo            // address to reach boost (or other) deal v 1.2 provider
	spActorAddr      address.Address           // address of the storage provider actor
	lotusAPI         v0api.FullNode            // Lotus API for determining deal start epoch and collateral bounds
	LighthouseAuth   string                    // Auth token to interact with Lighthouse Deal Engine
	lighthouseApiKey string                    // API key for lighthouse
	cleanup          func()                    // cleanup function to call on shutdown
}

// Define a Go struct to match the DataReady event from the OnRamp contract
type DataReadyEvent struct {
	Offer   Offer
	OfferID uint64
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

func (o *Offer) Piece() (filabi.PieceInfo, error) {
	pps := filabi.PaddedPieceSize(o.Size)
	if err := pps.Validate(); err != nil {
		return filabi.PieceInfo{}, err
	}
	_, c, err := cid.CidFromBytes(o.CommP)
	if err != nil {
		return filabi.PieceInfo{}, err
	}
	return filabi.PieceInfo{
		Size:     pps,
		PieceCID: c,
	}, nil
}

type AggregateTransfer struct {
	locations []string
	agg       *datasegment.Aggregate
}

type (
	LotusDaemonAPIClientV0 = v0api.FullNode
	LotusMinerAPIClientV0  = v0api.StorageMiner
	LotusBeaconEntry       = lotustypes.BeaconEntry
	LotusTS                = lotustypes.TipSet
	LotusTSK               = lotustypes.TipSetKey
)

// Function to start the aggregation service
func StartAggregationService(ctx context.Context, cfg *config.Config, srcCfg *config.SourceChainConfig) error {
	aggregator, err := NewAggregator(ctx, cfg, srcCfg)
	if err != nil {
		return err
	}
	return aggregator.run(ctx)
}

func NewAggregator(ctx context.Context, cfg *config.Config, srcCfg *config.SourceChainConfig) (*aggregator, error) {
	client, err := ethclient.Dial(srcCfg.Api)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Ethereum client for source chain at %s: %w", srcCfg.Api, err)
	}

	parsedABI, err := utils.LoadAbi(cfg.OnRampABIPath)
	if err != nil {
		return nil, err
	}
	proverContractAddress := common.HexToAddress(cfg.Destination.ProverAddr)
	onRampContractAddress := common.HexToAddress(srcCfg.OnRampAddress)
	payoutAddress := common.HexToAddress(cfg.PayoutAddr)
	onramp := bind.NewBoundContract(onRampContractAddress, *parsedABI, client, client, client)

	//aggregator need to call smart contract on source Chain to send podsi proof
	auth, err := utils.LoadPrivateKey(cfg, srcCfg.ChainID)
	if err != nil {
		return nil, err
	}
	// TODO consider allowing config to specify listen addr and pid, for now it shouldn't matter as boost will entertain anybody
	h, err := libp2p.New()
	if err != nil {
		return nil, err
	}

	lAPI, closer, err := NewLotusDaemonAPIClientV0(ctx, cfg.Destination.LotusAPI, 1, "")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the Ethereum client on the destination chain: using url %s: %v", cfg.Destination.LotusAPI, err)
	}

	// Get maddr for dialing boost from on chain miner actor
	providerAddr, err := address.NewFromString(cfg.ProviderAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse provider address: %w", err)
	}
	minfo, err := lAPI.StateMinerInfo(ctx, providerAddr, lotustypes.EmptyTSK)
	if err != nil {
		return nil, err
	}
	if minfo.PeerId == nil {
		return nil, fmt.Errorf("sp has no peer id set on chain")
	}
	var maddrs []multiaddr.Multiaddr
	for _, mma := range minfo.Multiaddrs {
		ma, err := multiaddr.NewMultiaddrBytes(mma)
		if err != nil {
			return nil, fmt.Errorf("storage provider %s had invalid multiaddrs in their info: %w", providerAddr, err)
		}
		maddrs = append(maddrs, ma)
	}
	if len(maddrs) == 0 {
		return nil, fmt.Errorf("storage provider %s has no multiaddrs set on-chain", providerAddr)
	}
	psPeerInfo := &peer.AddrInfo{
		ID:    *minfo.PeerId,
		Addrs: maddrs,
	}

	return &aggregator{
		client:           client,
		onramp:           onramp,
		onrampAddr:       onRampContractAddress,
		proverAddr:       proverContractAddress,
		payoutAddr:       payoutAddress,
		auth:             auth,
		ch:               make(chan DataReadyEvent, 1024), // buffer many events since consumer sometimes waits for chain
		transfers:        make(map[int]AggregateTransfer),
		transferLk:       sync.RWMutex{},
		transferAddr:     fmt.Sprintf("%s:%d", cfg.TransferIP, cfg.TransferPort),
		abi:              parsedABI,
		targetDealSize:   uint64(cfg.TargetAggSize),
		minDealSize:      uint64(cfg.MinDealSize),
		host:             h,
		spDealAddr:       psPeerInfo,
		spActorAddr:      providerAddr,
		lotusAPI:         lAPI,
		LighthouseAuth:   cfg.LighthouseAuth,
		lighthouseApiKey: cfg.LighthouseApiKey,
		cleanup: func() {
			closer()
			log.Printf("done with lotus api closer\n")
		},
	}, nil
}

// Run the two offerTaker persistant process
//  1. a goroutine listening for new DataReady events
//  2. a goroutine collecting data and aggregating before commiting
//     to store and sending to filecoin boost
func (a *aggregator) run(ctx context.Context) error {
	defer a.cleanup()
	g, ctx := errgroup.WithContext(ctx)
	// Start listening for events
	// New DataReady events are passed through the channel to aggregation handling
	g.Go(func() error {
		query := ethereum.FilterQuery{
			Addresses: []common.Address{a.onrampAddr},
			Topics:    [][]common.Hash{{a.abi.Events["DataReady"].ID}},
		}

		err := a.SubscribeQuery(ctx, query)
		for err == nil || strings.Contains(err.Error(), "read tcp") {
			if err != nil {
				log.Printf("ignoring mystery error: %s", err)
			}
			if ctx.Err() != nil {
				err = ctx.Err()
				break
			}
			err = a.SubscribeQuery(ctx, query)
		}
		log.Printf("context done exiting subscribe query\n")
		return err
	})

	// Start aggregatation event handling
	g.Go(func() error {
		return a.runAggregate(ctx)
	})

	// Start handling data transfer requests
	g.Go(func() error {
		http.HandleFunc("/", a.transferHandler)
		log.Printf("Data transfer server starting at %s\n", a.transferAddr)
		server := &http.Server{
			Addr:    a.transferAddr,
			Handler: nil, // http.DefaultServeMux
		}
		go func() {
			if err := server.ListenAndServe(); err != http.ErrServerClosed {
				log.Fatalf("Transfer HTTP server ListenAndServe: %v", err)
			}
		}()
		<-ctx.Done()
		log.Printf("context done about to shut down server\n")
		// Context is cancelled, shut down the server
		return server.Shutdown(context.Background())
	})

	return g.Wait()
}

func (a *aggregator) runAggregate(ctx context.Context) error {
	// pieces being aggregated, flushed upon commitment
	// Invariant: the pieces in the pending queue can always make a valid aggregate w.r.t a.targetDealSize
	fmt.Println("Start running aggregation.")
	var pending []DataReadyEvent
	total := uint64(0)

	for {
		select {
		case <-ctx.Done():
			log.Printf("ctx done shutting down aggregation")
			return nil
		case latestEvent := <-a.ch:
			{
				// Comment out to test
				// Check if the offer is too big to fit in a valid aggregate on its own
				// TODO: as referenced below there must be a better way when we introspect on the gory details of NewAggregate
				latestPiece, err := latestEvent.Offer.Piece()
				if err != nil {
					log.Printf("skipping offer %d, size %d not valid padded piece size ", latestEvent.OfferID, latestEvent.Offer.Size)
					continue
				}

				_, err = datasegment.NewAggregate(filabi.PaddedPieceSize(a.targetDealSize), []filabi.PieceInfo{
					latestPiece,
				})
				if err != nil {
					log.Printf("skipping offer %d, size %d exceeds max PODSI packable size", latestEvent.OfferID, latestEvent.Offer.Size)
					continue
				}
				pending = append(pending, latestEvent)

				// Turn offers into datasegment pieces
				pieces := make([]filabi.PieceInfo, len(pending))
				for i, event := range pending {
					piece, err := event.Offer.Piece()
					if err != nil {
						return err
					}
					pieces[i] = piece
				}

				// aggregation process
				aggregatePieces := pieces
				log.Println("Aggregated Pieces are:", aggregatePieces)
				_, size, err := datasegment.ComputeDealPlacement(aggregatePieces)
				if err != nil {
					panic(err)
				}
				overallSize := filabi.PaddedPieceSize(size)
				log.Printf("Aggregated Piece Size is %d", overallSize)

				next := 1 << (64 - bits.LeadingZeros64(uint64(overallSize+256)))
				if next <= int(a.minDealSize) {
					total += latestEvent.Offer.Size
					log.Printf("Offer-%d added. %d offers pending aggregation with total size=%d\n", latestEvent.OfferID, len(pending), total)
				} else {
					dealSize := filabi.PaddedPieceSize(next)
					a.targetDealSize = uint64(dealSize)
					log.Printf("Target DealSize is %d.", a.targetDealSize)

					agg, err := datasegment.NewAggregate(filabi.PaddedPieceSize(a.targetDealSize), aggregatePieces)
					if err != nil {
						return fmt.Errorf("failed to create aggregate from pending, should not be reachable: %w", err)
					}

					//Generates Podsi inclusion proof from aggregation
					inclProofs := make([]merkletree.ProofData, len(pieces))
					ids := make([]uint64, len(pieces))
					for i, piece := range pieces {
						podsi, err := agg.ProofForPieceInfo(piece)
						if err != nil {
							return err
						}
						ids[i] = pending[i].OfferID
						inclProofs[i] = podsi.ProofSubtree // Only do data proofs on chain for now not index proofs
					}

					//Sending aggCommp and inclusion proof to onramp contracts
					aggCommp, err := agg.PieceCID()
					if err != nil {
						return err
					}
					tx, err := a.onramp.Transact(a.auth, "commitAggregate", aggCommp.Bytes(), ids, inclProofs, a.payoutAddr)
					if err != nil {
						return err
					}
					receipt, err := bind.WaitMined(ctx, a.client, tx)
					if err != nil {
						return err
					}
					log.Printf("Tx %s committing aggregate commp %s included: %d", tx.Hash().Hex(), aggCommp.String(), receipt.Status)

					// Schedule aggregate data for transfer
					// After adding to the map this is now served in aggregator.transferHandler at `/?id={transferID}`
					locations := make([]string, len(pending))
					for i, event := range pending {
						locations[i] = event.Offer.Location
					}
					var transferID int
					a.transferLk.Lock()
					transferID = a.transferID
					a.transfers[transferID] = AggregateTransfer{
						locations: locations,
						agg:       agg,
					}
					a.transferID++
					a.transferLk.Unlock()
					log.Printf("Transfer ID %d scheduled for aggregation %s with %d urls.", transferID, aggCommp.String(), len(locations))

					// Aggregate data into a file
					homeDir, err := os.UserHomeDir()
					if err != nil {
						fmt.Println("Error:", err)
						return nil
					}
					aggLocation := filepath.Join(homeDir, "/.xchain/", aggCommp.String())
					err = a.saveAggregateToFile(transferID, aggLocation)
					if err != nil {
						log.Fatalf("failed to save aggregate to file: %s", err)
					} else {
						log.Println("Saved aggregated data into a file.")
					}

					// send file to lighthouse
					lhResp, err := buffer.UploadToLighthouse(aggLocation, a.lighthouseApiKey)
					if err != nil {
						log.Fatalf("failed to upload to lighthouse: %s", err)
					}
					retrievalURL := fmt.Sprintf("https://gateway.lighthouse.storage/ipfs/%s", lhResp.Hash)
					log.Printf("Uploaded CAR size is %s", lhResp.Size)

					// Make storage deal on Filecoin network.
					err = a.sendDeal(ctx, aggCommp, transferID, retrievalURL)
					if err != nil {
						log.Printf("[ERROR] failed to send deal: %s", err)
					}

					// Reset event log queue to empty
					pending = pending[:0]
				}
			}
		}
	}
}

// Send deal data to the configured SP deal making address (boost node)
// The deal is made with the configured prover client contract
// Heavily inspired by boost client
func (a *aggregator) sendDeal(ctx context.Context, aggCommp cid.Cid, transferID int, url string) error {
	if err := a.host.Connect(ctx, *a.spDealAddr); err != nil {
		return fmt.Errorf("failed to connect to peer %s: %w", a.spDealAddr.ID, err)
	}
	x, err := a.host.Peerstore().FirstSupportedProtocol(a.spDealAddr.ID, DealProtocolv120)
	if err != nil {
		return fmt.Errorf("getting protocols for peer %s: %w", a.spDealAddr.ID, err)
	}
	if len(x) == 0 {
		return fmt.Errorf("cannot make a deal with storage provider %s because it does not support protocol version 1.2.0", a.spDealAddr.ID)
	}

	// Construct deal
	dealUuid := uuid.New()
	log.Printf("making deal for commp=%s, UUID=%s\n", aggCommp.String(), dealUuid)

	if url == "" {
		url = fmt.Sprintf("http://%s/?id=%d", a.transferAddr, transferID)
	}

	transferParams := boosttypes2.HttpRequest{
		URL: url,
	}
	log.Printf("transfer URL: %s", url)
	paramsBytes, err := json.Marshal(transferParams)
	if err != nil {
		return fmt.Errorf("failed to marshal transfer params: %w", err)
	}
	transfer := boosttypes.Transfer{
		Type: "http",
		//ClientID: fmt.Sprintf("%d", transferID),
		Params: paramsBytes,
		Size:   a.targetDealSize - a.targetDealSize/128, // aggregate for transfer is not fr32 encoded
	}

	bounds, err := a.lotusAPI.StateDealProviderCollateralBounds(ctx, filabi.PaddedPieceSize(a.targetDealSize), false, lotustypes.EmptyTSK)
	if err != nil {
		return fmt.Errorf("failed to get collateral bounds: %w", err)
	}
	providerCollateral := fbig.Div(fbig.Mul(bounds.Min, fbig.NewInt(6)), fbig.NewInt(5)) // add 20% as boost client does
	tipset, err := a.lotusAPI.ChainHead(ctx)
	if err != nil {
		return fmt.Errorf("cannot get chain head: %w", err)
	}
	filHeight := tipset.Height()
	dealStart := filHeight + dealDelayEpochs
	dealEnd := dealStart + dealDuration
	filClient, err := address.NewDelegatedAddress(builtintypes.EthereumAddressManagerActorID, a.proverAddr[:])
	log.Printf("filClient = %s", filClient.String())
	if err != nil {
		return fmt.Errorf("failed to translate onramp address (%s) into a "+
			"Filecoin f4 address: %w", a.onrampAddr.Hex(), err)
	}
	chainID, err := a.client.ChainID(ctx)
	log.Printf("chainID = %d", chainID)
	if err != nil {
		return fmt.Errorf("failed to get chain ID: %w", err)
	}
	// Encode the chainID as uint256
	encodedChainID, err := utils.EncodeChainIDAsString(chainID)
	if err != nil {
		return fmt.Errorf("failed to encode chainID: %w", err)
	}
	dealLabel, err := market.NewLabelFromString(encodedChainID)
	if err != nil {
		return fmt.Errorf("failed to create deal label: %w", err)
	}
	log.Println("Start creating ClientDealProposal.")
	proposal := market.ClientDealProposal{
		Proposal: market.DealProposal{
			PieceCID:             aggCommp,
			PieceSize:            filabi.PaddedPieceSize(a.targetDealSize),
			VerifiedDeal:         true,
			Client:               filClient,
			Provider:             a.spActorAddr,
			Label:                dealLabel,
			StartEpoch:           dealStart,
			EndEpoch:             dealEnd,
			StoragePricePerEpoch: fbig.NewInt(0),
			ProviderCollateral:   providerCollateral,
		},
		// Signature is unchecked since client is smart contract
		ClientSignature: crypto.Signature{
			Type: crypto.SigTypeBLS,
			Data: []byte{0xc0, 0xff, 0xee},
		},
	}

	dealParams := boosttypes.DealParams{
		DealUUID:           dealUuid,
		ClientDealProposal: proposal,
		DealDataRoot:       aggCommp,
		IsOffline:          false,
		Transfer:           transfer,
		RemoveUnsealedCopy: false,
		SkipIPNIAnnounce:   false,
	}
	fmt.Println(dealParams.ClientDealProposal)
	log.Println("-------------------DealProposal Details----------------------")
	log.Println("DealUUID:", dealParams.DealUUID)
	log.Println("PieceCID:", proposal.Proposal.PieceCID.String())
	log.Println("PieceSize:", proposal.Proposal.PieceSize)
	log.Println("VerifiedDeal:", proposal.Proposal.VerifiedDeal)
	log.Println("Client:", proposal.Proposal.Client)
	log.Println("Provider:", proposal.Proposal.Provider)
	log.Println("Label:", proposal.Proposal.Label)
	log.Println("StartEpoch:", proposal.Proposal.StartEpoch)
	log.Println("EndEpoch:", proposal.Proposal.EndEpoch)
	log.Println("StoragePricePerEpoch:", proposal.Proposal.StoragePricePerEpoch)
	log.Println("ProviderCollateral:", proposal.Proposal.ProviderCollateral)
	log.Println("---------------------------------------------------------------")

	s, err := a.host.NewStream(ctx, a.spDealAddr.ID, DealProtocolv120)
	if err != nil {
		return err
	}
	defer s.Close()

	var resp boosttypes.DealResponse
	if err := doRpc(ctx, s, &dealParams, &resp); err != nil {
		return fmt.Errorf("send proposal rpc: %w", err)
	}
	if !resp.Accepted {
		return fmt.Errorf("deal proposal rejected: %s", resp.Message)
	}
	log.Printf("Deal UUID=%s is sent to miner %s.", dealUuid, a.spActorAddr)
	return nil
}

func doRpc(ctx context.Context, s inet.Stream, req interface{}, resp interface{}) error {
	errc := make(chan error)
	go func() {
		if err := cborutil.WriteCborRPC(s, req); err != nil {
			errc <- fmt.Errorf("failed to send request: %w", err)
			return
		}

		if err := cborutil.ReadCborRPC(s, resp); err != nil {
			errc <- fmt.Errorf("failed to read response: %w", err)
			return
		}

		errc <- nil
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *aggregator) SubscribeQuery(ctx context.Context, query ethereum.FilterQuery) error {
	logs := make(chan types.Log)
	log.Printf("Listening for data ready events on %s\n", a.onrampAddr.Hex())
	sub, err := a.client.SubscribeFilterLogs(ctx, query, logs)
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()

	// Map to track processed OfferIDs
	processed := make(map[uint64]struct{})
	// Mutex for thread safety
	var mu sync.Mutex
LOOP:
	for {
		select {
		case <-ctx.Done():
			break LOOP
		case err := <-sub.Err():
			return err
		case vLog := <-logs:
			log.Println("Receive a DataReady() event.")
			event, err := parseDataReadyEvent(vLog, a.abi)
			if err != nil {
				return err
			}

			// Deduplication logic with mutex
			mu.Lock()
			if _, exists := processed[event.OfferID]; exists {
				mu.Unlock() // Unlock and continue if duplicate
				log.Printf("Duplicate event ignored: Offer NO. %d\n", event.OfferID)
				continue
			}
			processed[event.OfferID] = struct{}{}
			mu.Unlock()

			log.Printf("Sending offer NO. %d for aggregation\n", event.OfferID)
			log.Printf("  Offer:\n")
			log.Printf("    CommP: %v\n", event.Offer.CommP)
			log.Printf("    Size: %d\n", event.Offer.Size)
			log.Printf("    Cid: %s\n", event.Offer.Cid)
			log.Printf("    Location: %s\n", event.Offer.Location)
			log.Printf("    Payment Token: %s\n", event.Offer.Token.Hex())      // Address needs .Hex() for printing
			log.Printf("    Payment Amount: %s\n", event.Offer.Amount.String()) // big.Int needs .String() for printing

			// This is where we should make packing decisions.
			// In the current prototype we accept all offers regardless
			// of payment type, amount or duration
			a.ch <- *event
		}
	}
	return nil
}

func (a *aggregator) saveAggregateToFile(trensferId int, location string) error {
	log.Printf("Saving aggregated data for transfer(%d) into a file:%s", trensferId, location)
	a.transferLk.RLock()
	transfer, ok := a.transfers[trensferId]
	a.transferLk.RUnlock()
	if !ok {
		return fmt.Errorf("no data found for ID %d", trensferId)
	}

	readers := []io.Reader{
		// bytes.NewReader(prefixCARBytes)
	}
	log.Printf("Fetching %d pieces from buffer.", len(transfer.locations))
	// Fetch each sub piece from its buffer location and add to readers
	for _, url := range transfer.locations {
		lazyReader := &lazyHTTPReader{url: url}
		readers = append(readers, lazyReader)
		defer lazyReader.Close()
	}
	aggReader, err := transfer.agg.AggregateObjectReader(readers)
	if err != nil {
		return fmt.Errorf("failed to create aggregate reader: %w", err)
	}

	// Create the file at the specified location
	file, err := os.Create(location)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Copy the aggregated data to the file
	_, err = io.Copy(file, aggReader)
	if err != nil {
		return fmt.Errorf("failed to write aggregate stream to file: %w", err)
	}

	return nil
}

// Handle data transfer requests from boost
func (a *aggregator) transferHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Received data transfer from boost.")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(int(a.targetDealSize-a.targetDealSize/128)))
	if r.Method == "HEAD" {
		w.WriteHeader(http.StatusOK)
		return
	}

	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		http.Error(w, "ID is required", http.StatusBadRequest)
		return
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	a.transferLk.RLock()
	transfer, ok := a.transfers[id]
	a.transferLk.RUnlock()
	if !ok {
		http.Error(w, "No data found", http.StatusNotFound)
		return
	}
	// First write the CAR prefix to the response
	prefixCARBytes, err := hex.DecodeString(prefixCAR)
	if err != nil {
		http.Error(w, "Failed to decode CAR prefix", http.StatusInternalServerError)
		return
	}

	readers := []io.Reader{bytes.NewReader(prefixCARBytes)}
	// Fetch each sub piece from its buffer location and write to response
	for _, url := range transfer.locations {
		lazyReader := &lazyHTTPReader{url: url}
		readers = append(readers, lazyReader)
		defer lazyReader.Close()
	}
	aggReader, err := transfer.agg.AggregateObjectReader(readers)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create aggregate reader: %s", err), http.StatusInternalServerError)
		return
	}
	_, err = io.Copy(w, aggReader)
	if err != nil {
		log.Printf("failed to write aggregate stream: %s", err)
	}
}

// LazyHTTPReader is an io.Reader that fetches data from an HTTP URL on the first Read call
type lazyHTTPReader struct {
	url     string
	reader  io.ReadCloser
	started bool
}

func (l *lazyHTTPReader) Read(p []byte) (int, error) {
	if !l.started {
		// Start the HTTP request on the first Read call
		log.Printf("reading %s\n", l.url)
		resp, err := http.Get(l.url)
		if err != nil {
			return 0, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return 0, fmt.Errorf("failed to fetch data: %s", resp.Status)
		}
		l.reader = resp.Body
		l.started = true
	}
	return l.reader.Read(p)
}

func (l *lazyHTTPReader) Close() error {
	if l.reader != nil {
		return l.reader.Close()
	}
	return nil
}

// Function to parse the DataReady event from log data
func parseDataReadyEvent(log types.Log, abi *abi.ABI) (*DataReadyEvent, error) {
	eventData, err := abi.Unpack("DataReady", log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack 'DataReady' event: %w", err)
	}

	// Assuming eventData is correctly ordered as per the event definition in the Solidity contract
	if len(eventData) != 2 {
		return nil, fmt.Errorf("unexpected number of fields for 'DataReady' event: got %d, want 2", len(eventData))
	}

	offerID, ok := eventData[1].(uint64)
	if !ok {
		return nil, fmt.Errorf("invalid type for offerID, expected uint64, got %T", eventData[1])
	}

	offerDataRaw := eventData[0]
	// JSON round trip to deserialize to offer
	bs, err := json.Marshal(offerDataRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal raw offer data to json: %w", err)
	}
	var offer Offer
	err = json.Unmarshal(bs, &offer)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal raw offer data to nice offer struct: %w", err)
	}

	return &DataReadyEvent{
		OfferID: offerID,
		Offer:   offer,
	}, nil
}

func NewLotusDaemonAPIClientV0(ctx context.Context, url string, timeoutSecs int, bearerToken string) (LotusDaemonAPIClientV0, jsonrpc.ClientCloser, error) {
	if timeoutSecs == 0 {
		timeoutSecs = 30
	}
	hdr := make(http.Header, 1)
	if bearerToken != "" {
		hdr["Authorization"] = []string{"Bearer " + bearerToken}
	}

	if !hasV0Suffix.MatchString(url) {
		url += "/rpc/v0"
	}

	c := new(v0api.FullNodeStruct)
	closer, err := jsonrpc.NewMergeClient(
		ctx,
		url,
		"Filecoin",
		[]interface{}{&c.Internal, &c.CommonStruct.Internal},
		hdr,
		// deliberately do not use jsonrpc.WithErrors(api.RPCErrors)
		jsonrpc.WithTimeout(time.Duration(timeoutSecs)*time.Second),
	)
	if err != nil {
		return nil, nil, err
	}
	return c, closer, nil
}

var hasV0Suffix = regexp.MustCompile(`\/rpc\/v0\/?\z`)
