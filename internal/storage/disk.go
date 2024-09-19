package storage

import (
	"fmt"
	generated "github.com/nebius/nebius-observability-agent-updater/generated/proto"
	"google.golang.org/protobuf/proto"
	"log/slog"
	"os"
)

func NewDiskStorage(filePath string, logger *slog.Logger) *DiskStorage {
	return &DiskStorage{
		filePath: filePath,
		logger:   logger,
	}
}

// DiskStorage handles saving and loading state from disk
type DiskStorage struct {
	filePath string
	logger   *slog.Logger
}

// SaveState saves the given state to disk
func (ds *DiskStorage) SaveState(state *generated.StorageState) error {
	ds.logger.Debug("Saving state", "path", ds.filePath)
	protoData, err := proto.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}
	return os.WriteFile(ds.filePath, protoData, 0755)
}

// LoadState loads the state from disk
func (ds *DiskStorage) LoadState() (*generated.StorageState, error) {
	ds.logger.Debug("Loading state", "path", ds.filePath)
	if _, err := os.Stat(ds.filePath); os.IsNotExist(err) {
		ds.logger.Info("State file does not exist", "path", ds.filePath)
		return nil, nil
	}
	protoData, err := os.ReadFile(ds.filePath)
	if err != nil {
		return nil, err
	}
	state := &generated.StorageState{}
	err = proto.Unmarshal(protoData, state)
	if err != nil {
		return nil, err
	}
	return state, nil
}
