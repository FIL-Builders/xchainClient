package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// DestinationChainConfig represents the Filecoin destination.
type DestinationChainConfig struct {
	ChainID    int    `json:"ChainID"`
	LotusAPI   string `json:"LotusAPI"`
	ProverAddr string `json:"ProverAddr"`
}

// SourceChainConfig represents a blockchain that can send data to Filecoin.
type SourceChainConfig struct {
	ChainID       int    `json:"ChainID"`
	Api           string `json:"Api"`
	OnRampAddress string `json:"OnRampAddress"`
}

// Config holds all configuration parameters.
type Config struct {
	Destination      DestinationChainConfig       `json:"destination"`
	Sources          map[string]SourceChainConfig `json:"sources"`
	KeyPath          string                       `json:"KeyPath"`
	ClientAddr       string                       `json:"ClientAddr"`
	PayoutAddr       string                       `json:"PayoutAddr"`
	OnRampABIPath    string                       `json:"OnRampABIPath"`
	BufferPath       string                       `json:"BufferPath"`
	BufferPort       int                          `json:"BufferPort"`
	ProviderAddr     string                       `json:"ProviderAddr"`
	LighthouseApiKey string                       `json:"LighthouseApiKey"`
	LighthouseAuth   string                       `json:"LighthouseAuth"`
	TransferIP       string                       `json:"TransferIP"`
	TransferPort     int                          `json:"TransferPort"`
	TargetAggSize    int                          `json:"TargetAggSize"`
	MinDealSize      int                          `json:"MinDealSize"`
}

// LoadConfig reads the configuration from a JSON file.
func LoadConfig(path string) (*Config, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	return &cfg, nil
}

// GetSourceConfig retrieves a source chain's configuration by its name.
func GetSourceConfig(cfg *Config, network string) (*SourceChainConfig, error) {
	if srcCfg, exists := cfg.Sources[network]; exists {
		return &srcCfg, nil
	}
	return nil, fmt.Errorf("source chain configuration for '%s' not found", network)
}
