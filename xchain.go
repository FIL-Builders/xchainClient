package main

import (
	"github.com/FIL-Builders/xchainClient/config"

	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"path/filepath"

	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
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
	"github.com/mitchellh/go-homedir"
	"github.com/multiformats/go-multiaddr"
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:        "xchain",
		Description: "Filecoin Xchain Data Services",
		Usage:       "Export filecoin data storage to any blockchain",
		Commands: []*cli.Command{
			{
				Name:  "daemon",
				Usage: "Start the xchain adapter daemon",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "config",
						Usage: "Path to the configuration file",
						Value: "./config/config.json",
					},
					&cli.StringFlag{
						Name:     "chain",
						Usage:    "Name of the source blockchain (e.g., ethereum, polygon)",
						Required: true,
					},
					&cli.BoolFlag{
						Name:  "buffer-service",
						Usage: "Run a buffer server",
						Value: false,
					},
					&cli.BoolFlag{
						Name:  "aggregation-service",
						Usage: "Run an aggregation server",
						Value: false,
					},
				},
				Action: func(cctx *cli.Context) error {
					isBuffer := cctx.Bool("buffer-service")
					isAgg := cctx.Bool("aggregation-service")
					if !isBuffer && !isAgg { // default to running aggregator
						isAgg = true
					}

					cfg, err := config.LoadConfig(cctx.String("config"))
					if err != nil {
						log.Fatal(err)
					}

					// Get source chain name
					chainName := cctx.String("chain")
					srcCfg, err := config.GetSourceConfig(cfg, chainName)
					if err != nil {
						log.Fatalf("Invalid chain name '%s': %v", chainName, err)
					}

					g, ctx := errgroup.WithContext(cctx.Context)
					fmt.Println("buffer-service is ", isBuffer)
					fmt.Println("aggregation-service is ", isAgg)
					g.Go(func() error {
						if !isBuffer {
							return nil
						}
						path, err := homedir.Expand(cfg.BufferPath)
						if err != nil {
							return err
						}
						if err := os.MkdirAll(path, os.ModePerm); err != nil {
							return err
						}

						srv, err := NewBufferHTTPService(cfg.BufferPath)
						if err != nil {
							return &http.MaxBytesError{}
						}
						http.HandleFunc("/put", srv.PutHandler)
						http.HandleFunc("/get", srv.GetHandler)

						log.Printf("Server starting on port %d\n", cfg.BufferPort)
						server := &http.Server{
							Addr:    fmt.Sprintf("0.0.0.0:%d", cfg.BufferPort),
							Handler: nil, // http.DefaultServeMux
						}
						go func() {
							if err := server.ListenAndServe(); err != http.ErrServerClosed {
								log.Fatalf("Buffer HTTP server ListenAndServe: %v", err)
							}
						}()
						<-ctx.Done()

						// Context is cancelled, shut down the server
						return server.Shutdown(context.Background())
					})
					g.Go(func() error {
						if !isAgg {
							return nil
						}
						a, err := NewAggregator(ctx, cfg, srcCfg)
						if err != nil {
							return err
						}
						return a.run(ctx)
					})
					return g.Wait()
				},
			},
			{
				Name:  "client",
				Usage: "Send car file from cross chain to filecoin",
				Subcommands: []*cli.Command{
					{
						Name:      "offer-file",
						Usage:     "Offer data by providing a file and payment parameters (file is pre-processed automatically)",
						ArgsUsage: "<file_path> <payment-addr> <payment-amount>",
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:  "config",
								Usage: "Path to the configuration file",
								Value: "./config/config.json",
							},
							&cli.StringFlag{
								Name:     "chain",
								Usage:    "Name of the source blockchain (e.g., ethereum, polygon)",
								Required: true,
							},
						},
						Action: offerFileAction,
					},
					{
						Name:      "offer-car",
						Usage:     "Offer data by providing file and payment parameters",
						ArgsUsage: "<commP> <size> <cid> <bufferLocation> <token-hex> <token-amount>",
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:  "config",
								Usage: "Path to the configuration file",
								Value: "./config/config.json",
							},
							&cli.StringFlag{
								Name:     "chain",
								Usage:    "Name of the source blockchain (e.g., ethereum, polygon)",
								Required: true,
							},
						},
						Action: func(cctx *cli.Context) error {
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
							parsedABI, err := LoadAbi(cfg.OnRampABIPath)
							if err != nil {
								log.Fatal(err)
							}
							onramp := bind.NewBoundContract(contractAddress, *parsedABI, client, client, client)
							if err != nil {
								log.Fatal(err)
							}

							// Get auth
							auth, err := loadPrivateKey(cfg)
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
						},
					},
				},
			},
			{
				Name:  "generate-account",
				Usage: "Generate a new Ethereum keystore account",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "keystore-file",
						Usage:    "Path to the keystore JSON file (must end in .json)",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "password",
						Usage:    "Password to encrypt the keystore file",
						Required: true,
					},
				},
				Action: func(cctx *cli.Context) error {
					keystoreFile := cctx.String("keystore-file")
					password := cctx.String("password")

					// Validate and create keystore
					accountAddress, err := generateEthereumAccount(keystoreFile, password)
					if err != nil {
						log.Fatalf("Error generating account: %v", err)
					}

					// Output generated account info
					fmt.Println("New Ethereum account created!")
					fmt.Println("Address:", accountAddress)
					fmt.Println("Keystore File Path:", keystoreFile)

					return nil
				},
			},
		},
	}
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	go func() {
		<-signalChan
		fmt.Println("Ctrl-c received. Shutting down...")
		os.Exit(0)
	}()

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
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

type aggregator struct {
	client         *ethclient.Client         // raw client for log subscriptions
	onramp         *bind.BoundContract       // onramp binding over raw client for message sending
	auth           *bind.TransactOpts        // auth for message sending
	abi            *abi.ABI                  // onramp abi for log subscription and message sending
	onrampAddr     common.Address            // onramp address for log subscription
	proverAddr     common.Address            // prover address for client contract deal
	payoutAddr     common.Address            // aggregator payout address for receiving funds
	ch             chan DataReadyEvent       // pass events to seperate goroutine for processing
	transfers      map[int]AggregateTransfer // track aggregate data awaiting transfer
	transferLk     sync.RWMutex              // Mutex protecting transfers map
	transferID     int                       // ID of the next transfer
	transferAddr   string                    // address to listen for transfer requests
	targetDealSize uint64                    // how big aggregates should be
	host           host.Host                 // libp2p host for deal protocol to boost
	spDealAddr     *peer.AddrInfo            // address to reach boost (or other) deal v 1.2 provider
	spActorAddr    address.Address           // address of the storage provider actor
	lotusAPI       v0api.FullNode            // Lotus API for determining deal start epoch and collateral bounds
	LighthouseAuth string                    // Auth token to interact with Lighthouse Deal Engine
	cleanup        func()                    // cleanup function to call on shutdown
}

// Thank you @ribasushi
type (
	LotusDaemonAPIClientV0 = v0api.FullNode
	LotusMinerAPIClientV0  = v0api.StorageMiner
	LotusBeaconEntry       = lotustypes.BeaconEntry
	LotusTS                = lotustypes.TipSet
	LotusTSK               = lotustypes.TipSetKey
)

var hasV0Suffix = regexp.MustCompile(`\/rpc\/v0\/?\z`)

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

func NewAggregator(ctx context.Context, cfg *config.Config, srcCfg *config.SourceChainConfig) (*aggregator, error) {
	client, err := ethclient.Dial(srcCfg.Api)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Ethereum client for source chain at %s: %w", srcCfg.Api, err)
	}

	parsedABI, err := LoadAbi(cfg.OnRampABIPath)
	if err != nil {
		return nil, err
	}
	proverContractAddress := common.HexToAddress(cfg.Destination.ProverAddr)
	onRampContractAddress := common.HexToAddress(srcCfg.OnRampAddress)
	payoutAddress := common.HexToAddress(cfg.PayoutAddr)
	onramp := bind.NewBoundContract(onRampContractAddress, *parsedABI, client, client, client)

	auth, err := loadPrivateKey(cfg)
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
		client:         client,
		onramp:         onramp,
		onrampAddr:     onRampContractAddress,
		proverAddr:     proverContractAddress,
		payoutAddr:     payoutAddress,
		auth:           auth,
		ch:             make(chan DataReadyEvent, 1024), // buffer many events since consumer sometimes waits for chain
		transfers:      make(map[int]AggregateTransfer),
		transferLk:     sync.RWMutex{},
		transferAddr:   fmt.Sprintf("%s:%d", cfg.TransferIP, cfg.TransferPort),
		abi:            parsedABI,
		targetDealSize: uint64(cfg.TargetAggSize),
		host:           h,
		spDealAddr:     psPeerInfo,
		spActorAddr:    providerAddr,
		lotusAPI:       lAPI,
		LighthouseAuth: cfg.LighthouseAuth,
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
		log.Printf("Server starting on port %d\n", transferPort)
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
	// Data transfer port
	transferPort = 1728
	// libp2p identifier for latest deal protocol
	DealProtocolv120 = "/fil/storage/mk/1.2.0"
	// Delay to start deal at. For 2k devnet 4 second block time this is 13.3 minutes TODO Config
	dealDelayEpochs = 200
	// Storage deal duration, TODO figure out what to do about this, either comes from offer or config
	dealDuration = 518400 // 6 months (on mainnet)
)

func (a *aggregator) runAggregate(ctx context.Context) error {
	// pieces being aggregated, flushed upon commitment
	// Invariant: the pieces in the pending queue can always make a valid aggregate w.r.t a.targetDealSize
	fmt.Println("Start running aggregation.")
	pending := make([]DataReadyEvent, 0, 256)
	total := uint64(0)
	prefixPiece := filabi.PieceInfo{
		Size:     filabi.PaddedPieceSize(prefixCARSizePadded),
		PieceCID: cid.MustParse(prefixCARCid),
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("ctx done shutting down aggregation")
			return nil
		case latestEvent := <-a.ch:
			// Check if the offer is too big to fit in a valid aggregate on its own
			// TODO: as referenced below there must be a better way when we introspect on the gory details of NewAggregate
			latestPiece, err := latestEvent.Offer.Piece()
			if err != nil {
				log.Printf("skipping offer %d, size %d not valid padded piece size ", latestEvent.OfferID, latestEvent.Offer.Size)
				continue
			}
			_, err = datasegment.NewAggregate(filabi.PaddedPieceSize(a.targetDealSize), []filabi.PieceInfo{
				prefixPiece,
				latestPiece,
			})
			if err != nil {
				log.Printf("error creating aggregate: %s", err)
				continue
			}
			// TODO: in production we'll maybe want to move data from buffer before we commit to storing it.

			// TODO: Unsorted greedy is a very naive knapsack strategy, production will want something better
			// TODO: doing all the work of creating an aggregate for every new offer is quite wasteful
			//      there must be a cheaper way to do this, but for now it is the most expediant without learning
			//      all the gory edge cases in NewAggregate

			// Turn offers into datasegment pieces
			pieces := make([]filabi.PieceInfo, len(pending)+1)
			for i, event := range pending {
				piece, err := event.Offer.Piece()
				if err != nil {
					return err
				}
				pieces[i] = piece
			}

			pieces[len(pending)] = latestPiece
			// aggregate
			aggregatePieces := append([]filabi.PieceInfo{
				prefixPiece,
			}, pieces...)
			_, err = datasegment.NewAggregate(filabi.PaddedPieceSize(a.targetDealSize), aggregatePieces)
			if err != nil { // we've overshot, lets commit to just pieces in pending
				total = 0
				// Remove the latest offer which took us over
				pieces = pieces[:len(pieces)-1]
				aggregatePieces = aggregatePieces[:len(aggregatePieces)-1]
				agg, err := datasegment.NewAggregate(filabi.PaddedPieceSize(a.targetDealSize), aggregatePieces)
				if err != nil {
					return fmt.Errorf("failed to create aggregate from pending, should not be reachable: %w", err)
				}

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
				log.Printf("Transfer ID %d scheduled for aggregate %s", transferID, aggCommp.String())

				err = a.sendDeal(ctx, aggCommp, transferID)
				if err != nil {
					log.Printf("[ERROR] failed to send deal: %s", err)
				}

				// Reset queue to empty, add the event that triggered aggregation
				pending = pending[:0]
				pending = append(pending, latestEvent)

			} else {
				total += latestEvent.Offer.Size
				pending = append(pending, latestEvent)
				log.Printf("Offer %d added. %d offers pending aggregation with total size=%d\n", latestEvent.OfferID, len(pending), total)
			}
		}
	}
}

// Send deal data to the configured SP deal making address (boost node)
// The deal is made with the configured prover client contract
// Heavily inspired by boost client
func (a *aggregator) sendDeal(ctx context.Context, aggCommp cid.Cid, transferID int) error {
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
	log.Printf("making deal for commp %s, UUID=%s\n", aggCommp.String(), dealUuid)
	transferParams := boosttypes2.HttpRequest{
		URL: fmt.Sprintf("http://%s/?id=%d", a.transferAddr, transferID),
	}
	paramsBytes, err := json.Marshal(transferParams)
	if err != nil {
		return fmt.Errorf("failed to marshal transfer params: %w", err)
	}
	transfer := boosttypes.Transfer{
		Type:     "http",
		ClientID: fmt.Sprintf("%d", transferID),
		Params:   paramsBytes,
		Size:     a.targetDealSize - a.targetDealSize/128, // aggregate for transfer is not fr32 encoded
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
	if err != nil {
		return fmt.Errorf("failed to translate onramp address (%s) into a "+
			"Filecoin f4 address: %w", a.onrampAddr.Hex(), err)
	}
	chainID, err := a.client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get chain ID: %w", err)
	}
	// Encode the chainID as uint256
	encodedChainID, err := encodeChainID(chainID)
	if err != nil {
		return fmt.Errorf("failed to encode chainID: %w", err)
	}
	dealLabel, err := market.NewLabelFromBytes(encodedChainID)
	if err != nil {
		return fmt.Errorf("failed to create deal label: %w", err)
	}
	proposal := market.ClientDealProposal{
		Proposal: market.DealProposal{
			PieceCID:             aggCommp,
			PieceSize:            filabi.PaddedPieceSize(a.targetDealSize),
			VerifiedDeal:         false,
			Client:               filClient,
			Provider:             a.spActorAddr,
			StartEpoch:           dealStart,
			EndEpoch:             dealEnd,
			StoragePricePerEpoch: fbig.NewInt(0),
			ProviderCollateral:   providerCollateral,
			Label:                dealLabel,
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

// Handle data transfer requests from boost
func (a *aggregator) transferHandler(w http.ResponseWriter, r *http.Request) {
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

type AggregateTransfer struct {
	locations []string
	agg       *datasegment.Aggregate
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
			log.Printf("    Offer: %v\n", event.Offer.CommP)
			log.Printf("    Size: %d\n", event.Offer.Size)
			log.Printf("    Cid: %s\n", event.Offer.Cid)
			log.Printf("    Location: %s\n", event.Offer.Location)
			log.Printf("    Amount: %s\n", event.Offer.Amount.String()) // big.Int needs .String() for printing
			log.Printf("    Token: %s\n", event.Offer.Token.Hex())      // Address needs .Hex() for printing

			// This is where we should make packing decisions.
			// In the current prototype we accept all offers regardless
			// of payment type, amount or duration
			a.ch <- *event
		}
	}
	return nil
}

// Define a Go struct to match the DataReady event from the OnRamp contract
type DataReadyEvent struct {
	Offer   Offer
	OfferID uint64
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

func HandleOffer(offer *Offer) error {
	return nil
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

// Load and unlock the keystore with XCHAIN_PASSPHRASE env var
// return a transaction authorizer
func loadPrivateKey(cfg *config.Config) (*bind.TransactOpts, error) {
	path, err := homedir.Expand(cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open keystore file: %w", err)
	}
	defer file.Close()
	keyJSON, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read key store bytes from file: %w", err)
	}

	// Create a temporary directory to initialize the per-call keystore
	tempDir, err := os.MkdirTemp("", "xchain-tmp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	ks := keystore.NewKeyStore(tempDir, keystore.StandardScryptN, keystore.StandardScryptP)

	// Import existing key
	passphrase := os.Getenv("XCHAIN_PASSPHRASE")
	if passphrase == "" {
		return nil, errors.New("environment variable XCHAIN_PASSPHRASE is not set or empty")
	}

	a, err := ks.Import(keyJSON, passphrase, passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to import key %s: %w", cfg.ClientAddr, err)
	}
	if err := ks.Unlock(a, passphrase); err != nil {
		return nil, fmt.Errorf("failed to unlock keystore: %w", err)
	}

	return bind.NewKeyStoreTransactorWithChainID(ks, a, big.NewInt(int64(cfg.Destination.ChainID)))
}

// Load contract abi at the given path
func LoadAbi(path string) (*abi.ABI, error) {
	path, err := homedir.Expand(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open abi file: %w", err)
	}
	parsedABI, err := abi.JSON(f)
	if err != nil {
		return nil, fmt.Errorf("failed to parse abi: %w", err)
	}
	return &parsedABI, nil
}

func encodeChainID(chainID *big.Int) ([]byte, error) {
	// Define the ABI arguments
	uint256Type, err := abi.NewType("uint256", "", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create uint256 type: %w", err)
	}

	arguments := abi.Arguments{
		{Type: uint256Type}, // chainID is a uint256 in Solidity
	}

	// Pack the chainID into a byte array
	data, err := arguments.Pack(chainID)
	if err != nil {
		return nil, fmt.Errorf("failed to encode chainID: %w", err)
	}

	return data, nil
}

// generateEthereumAccount creates a new Ethereum account and saves it to the specified JSON file
func generateEthereumAccount(keystoreFile, password string) (string, error) {
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
