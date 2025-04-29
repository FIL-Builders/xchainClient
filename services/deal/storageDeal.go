package deal

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/FIL-Builders/xchainClient/config"
	"github.com/google/uuid"
)

// DealInfo represents the details of a storage deal
type DealInfo struct {
	DealUUID     uuid.UUID `json:"dealUUID"`
	PieceCID     string    `json:"pieceCID"`
	Provider     string    `json:"provider"`
	Client       string    `json:"client"`
	Size         uint64    `json:"size"`
	StartEpoch   int64     `json:"startEpoch"`
	EndEpoch     int64     `json:"endEpoch"`
	TransferID   int       `json:"transferID"`
	RetrievalURL string    `json:"retrievalURL"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	LastChecked  time.Time `json:"lastChecked"`
}

// GetDealByUUID retrieves deal information by UUID
func GetDealByUUID(dealUUID string) (*DealInfo, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	filename := filepath.Join(homeDir, ".xchain", "deals", fmt.Sprintf("%s.json", dealUUID))
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read deal file: %w", err)
	}

	var dealInfo DealInfo
	if err := json.Unmarshal(data, &dealInfo); err != nil {
		return nil, fmt.Errorf("failed to unmarshal deal info: %w", err)
	}

	return &dealInfo, nil
}

// ListAllDeals returns a list of all recorded deals
func ListAllDeals() ([]DealInfo, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	dealsDir := filepath.Join(homeDir, ".xchain", "deals")
	files, err := ioutil.ReadDir(dealsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read deals directory: %w", err)
	}

	var deals []DealInfo
	for _, file := range files {
		if filepath.Ext(file.Name()) != ".json" {
			continue
		}

		data, err := ioutil.ReadFile(filepath.Join(dealsDir, file.Name()))
		if err != nil {
			log.Printf("Warning: Failed to read deal file %s: %v", file.Name(), err)
			continue
		}

		var deal DealInfo
		if err := json.Unmarshal(data, &deal); err != nil {
			log.Printf("Warning: Failed to unmarshal deal file %s: %v", file.Name(), err)
			continue
		}

		deals = append(deals, deal)
	}

	return deals, nil
}

// UpdateDealStatus updates the status of a deal
func UpdateDealStatus(dealUUID string, status string) error {
	deal, err := GetDealByUUID(dealUUID)
	if err != nil {
		return err
	}

	deal.Status = status
	deal.LastChecked = time.Now()

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	filename := filepath.Join(homeDir, ".xchain", "deals", fmt.Sprintf("%s.json", dealUUID))
	dealData, err := json.MarshalIndent(deal, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal deal info: %w", err)
	}

	if err := os.WriteFile(filename, dealData, 0644); err != nil {
		return fmt.Errorf("failed to write deal info file: %w", err)
	}

	return nil
}

// SmartContractDeal continuously runs logic until the context is canceled
func SmartContractDeal(ctx context.Context, cfg *config.Config, srcCfg *config.SourceChainConfig) error {
	log.Println("Starting SmartContractDeal process...")

	// Example: Perform a periodic task in a loop until the context is canceled
	ticker := time.NewTicker(5 * time.Second) // Adjust the interval as needed
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Handle graceful shutdown
			log.Println("SmartContractDeal process is shutting down...")
			return nil
		case <-ticker.C:
			// Example logic: Interact with a smart contract or process data
			err := processSmartContractLogic(cfg, srcCfg)
			if err != nil {
				log.Printf("Error in SmartContractDeal process: %v", err)
			} else {
				log.Println("SmartContractDeal task completed successfully")
			}
		}
	}
}

// processSmartContractLogic handles the core logic of the SmartContractDeal process
func processSmartContractLogic(cfg *config.Config, srcCfg *config.SourceChainConfig) error {
	// Example placeholder logic:
	// This is where you could interact with a smart contract, fetch data, or perform computations.

	log.Printf("Processing smart contract logic with chain: %s and config: %+v", srcCfg.OnRampAddress, cfg)

	// TODO: Implement actual logic to interact with a smart contract
	// - Call an Ethereum or Filecoin smart contract
	// - Process events or data
	// - Execute business logic

	return nil // Return an error if something fails
}
