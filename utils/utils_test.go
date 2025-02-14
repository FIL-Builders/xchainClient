package utils

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"testing"

	"github.com/FIL-Builders/xchainClient/config"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/assert"
)

// Test function to test chainID encoding
func TestChainIdEncoding(t *testing.T) {
	configPath := "./config/config.json" // Replace with the actual path to your config file

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to unmarshal config: %v", err)
	}

	// Get source chain name
	chainName := "avalanche"
	srcCfg, err := config.GetSourceConfig(cfg, chainName)
	if err != nil {
		log.Fatalf("Invalid chain name '%s': %v", chainName, err)
	}

	// Connect to the Ethereum client
	client, err := ethclient.Dial(srcCfg.Api)
	if err != nil {
		t.Fatalf("failed to connect to Ethereum client for source chain %s at %s: %v", chainName, srcCfg.Api, err)
	}

	// Query the chain ID
	chainID, err := client.ChainID(context.Background())
	if err != nil {
		t.Fatalf("failed to query source chain %s at %s to get chain ID: %v", chainName, srcCfg.Api, err)
	}

	// Encode the chainID
	encodedChainID, err := EncodeChainID(chainID)
	if err != nil {
		t.Fatalf("failed to encode chainID: %v", err)
	}

	fmt.Println("Encoded chainID: ", encodedChainID)
	fmt.Println("chainID: ", chainID)
	hexEncodedChainID := hex.EncodeToString(encodedChainID)
	fmt.Printf("Encoded ChainID in Hex: %s\n", hexEncodedChainID)
	decodedChainID, err := decodeChainID(encodedChainID)
	if err != nil {
		t.Fatalf("failed to decode chainID: %v", err)
	}
	fmt.Println("Decoded chainID: ", decodedChainID)
	assert.Equal(t, decodedChainID, chainID, "Encoded chainID does not match expected value")
}

func decodeChainID(data []byte) (*big.Int, error) {
	// Define the ABI arguments
	uint256Type, err := abi.NewType("uint256", "", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create uint256 type: %w", err)
	}

	arguments := abi.Arguments{
		{Type: uint256Type}, // chainID is a uint256 in Solidity
	}

	// Unpack the byte array into a slice of interface{}
	unpacked, err := arguments.Unpack(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode chainID: %w", err)
	}

	// Extract the chainID from the unpacked slice
	if len(unpacked) == 0 {
		return nil, fmt.Errorf("no data unpacked")
	}

	chainID, ok := unpacked[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("failed to assert type to *big.Int")
	}

	return chainID, nil
}
