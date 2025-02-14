package main

import (
	"github.com/FIL-Builders/xchainClient/config"
	"github.com/FIL-Builders/xchainClient/services/aggregator"
	"github.com/FIL-Builders/xchainClient/services/buffer"
	"github.com/FIL-Builders/xchainClient/services/client"
	"github.com/FIL-Builders/xchainClient/services/deal"

	"fmt"
	"log"
	"os"
	"os/signal"

	"golang.org/x/sync/errgroup"

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
						if isBuffer {
							return buffer.StartBufferService(ctx, cfg)
						}
						return nil

					})
					g.Go(func() error {
						if isAgg {
							return aggregator.StartAggregationService(ctx, cfg, srcCfg)
						}
						return nil
					})
					g.Go(func() error {
						if !isAgg && !isBuffer {
							return deal.SmartContractDeal(ctx, cfg, srcCfg)
						}
						return nil
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
						Action: client.OfferFileAction,
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
						Action: client.OfferCarAction,
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
					accountAddress, err := client.GenerateEthereumAccount(keystoreFile, password)
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
