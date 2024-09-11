package config

import (
	"github.com/nebius/nebius-observability-agent-updater/internal/client"
	"github.com/nebius/nebius-observability-agent-updater/internal/loggerhelper"
	"github.com/nebius/nebius-observability-agent-updater/internal/metadata"
	"time"
)

type Config struct {
	StatePath    string                 `yaml:"state_path"`
	PollInterval time.Duration          `yaml:"poll_interval"`
	PollJitter   time.Duration          `yaml:"poll_jitter"`
	Metadata     metadata.Config        `yaml:"metadata"`
	GRPC         client.GRPCConfig      `yaml:"grpc"`
	Logger       loggerhelper.LogConfig `yaml:"logger"`
}

func GetDefaultConfig() *Config {
	return &Config{
		StatePath: "/var/lib/nebius-observability-agent-updater/state",
		//PollInterval: 60 * time.Minute,
		PollInterval: 5 * time.Second,
		PollJitter:   0,
		Metadata: metadata.Config{
			Path:               "/tmp/cloud-metadata",
			ParentIdFilename:   "parent-id",
			InstanceIdFilename: "instance-id",
		},
		GRPC: client.GRPCConfig{
			Endpoint: "beta.agent-manager.teplo.eu-north1.nebius.cloud:443",
			Insecure: false,
			Timeout:  5 * time.Second,
			Retry: client.RetryConfig{
				MaxElapsedTime:      time.Second * 30,
				InitialInterval:     time.Second,
				Multiplier:          1.5,
				RandomizationFactor: 0.5,
			},
		},
		Logger: loggerhelper.LogConfig{
			LogLevel: "DEBUG",
		},
	}
}
