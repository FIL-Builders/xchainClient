package utils

import (
	"errors"
	"fmt"
	"math/big"
	"os"

	"github.com/FIL-Builders/xchainClient/config"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/mitchellh/go-homedir"
)

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

func EncodeChainID(chainID *big.Int) ([]byte, error) {
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

// Load and unlock the keystore with XCHAIN_PASSPHRASE env var
// return a transaction authorizer
func LoadPrivateKey(cfg *config.Config) (*bind.TransactOpts, error) {
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
