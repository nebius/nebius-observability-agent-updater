package config

import (
	"github.com/nebius/nebius-observability-agent-updater/internal/metadata"
	"time"
)

type Config struct {
	StatePath    string          `yaml:"state_path"`
	PollInterval time.Duration   `yaml:"poll_interval"`
	PollJitter   time.Duration   `yaml:"poll_jitter"`
	Metadata     metadata.Config `yaml:"metadata"`
}

func GetDefaultConfig() *Config {
	return &Config{
		StatePath:    "/var/lib/nebius-observability-agent-updater/state",
		PollInterval: 60 * time.Minute,
		PollJitter:   time.Minute,
		Metadata: metadata.Config{
			Path:               "/tmp/cloud-metadata",
			ParentIdFilename:   "parent-id",
			InstanceIdFilename: "instance-id",
		},
	}
}
