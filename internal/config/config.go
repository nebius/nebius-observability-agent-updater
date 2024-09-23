package config

import (
	"github.com/nebius/nebius-observability-agent-updater/internal/client"
	"github.com/nebius/nebius-observability-agent-updater/internal/loggerhelper"
	"github.com/nebius/nebius-observability-agent-updater/internal/metadata"
	"time"
)

type Config struct {
	PollInterval time.Duration          `yaml:"poll_interval"`
	PollJitter   time.Duration          `yaml:"poll_jitter"`
	Metadata     metadata.Config        `yaml:"metadata"`
	GRPC         client.GRPCConfig      `yaml:"grpc"`
	Logger       loggerhelper.LogConfig `yaml:"logger"`
}

func GetDefaultConfig() *Config {
	return &Config{
		PollInterval: time.Minute,
		PollJitter:   30 * time.Second,
		Metadata: metadata.Config{
			Path:               "/mnt/cloud-metadata",
			ParentIdFilename:   "parent-id",
			InstanceIdFilename: "instance-id",
			IamTokenFilename:   "tsa-token",
		},
		GRPC: client.GetDefaultGrpcConfig(),
		Logger: loggerhelper.LogConfig{
			LogLevel: "INFO",
		},
	}
}
