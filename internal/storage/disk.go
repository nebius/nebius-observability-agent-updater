package storage

import (
	"fmt"
	generated "github.com/nebius/nebius-observability-agent-updater/generated/proto"
	"google.golang.org/protobuf/proto"
	"os"
)

func NewDiskStorage(filePath string) *DiskStorage {
	return &DiskStorage{
		filePath: filePath,
	}
}

// DiskStorage handles saving and loading state from disk
type DiskStorage struct {
	filePath string
}

// SaveState saves the given state to disk
func (ds *DiskStorage) SaveState(state *generated.StorageState) error {
	protoData, err := proto.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}
	return os.WriteFile(ds.filePath, protoData, 0755)
}

// LoadState loads the state from disk
func (ds *DiskStorage) LoadState() (*generated.StorageState, error) {
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
