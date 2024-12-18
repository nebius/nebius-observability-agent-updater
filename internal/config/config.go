package config

import (
	"github.com/nebius/nebius-observability-agent-updater/internal/client/clientconfig"
	"github.com/nebius/nebius-observability-agent-updater/internal/loggerhelper"
	"github.com/nebius/nebius-observability-agent-updater/internal/metadata"
	"time"
)

type Config struct {
	PollInterval         time.Duration           `yaml:"poll_interval"`
	PollJitter           time.Duration           `yaml:"poll_jitter"`
	Metadata             metadata.Config         `yaml:"metadata"`
	GRPC                 clientconfig.GRPCConfig `yaml:"grpc"`
	Logger               loggerhelper.LogConfig  `yaml:"logger"`
	UpdateRepoScriptPath string                  `yaml:"update_repo_script_path"`
	Mk8sClusterIdPath    string                  `yaml:"mk8s_cluster_id_path"`
}

func GetDefaultConfig() *Config {
	return &Config{
		UpdateRepoScriptPath: "/usr/sbin/nebius-update-repo.sh",
		PollInterval:         time.Minute,
		PollJitter:           30 * time.Second,
		Metadata: metadata.Config{
			Path:               "/mnt/cloud-metadata",
			ParentIdFilename:   "parent-id",
			InstanceIdFilename: "instance-id",
			IamTokenFilename:   "tsa-token",
		},
		Mk8sClusterIdPath: "/usr/local/etc/mk8s-cluster-id",
		GRPC:              clientconfig.GetDefaultGrpcConfig(),
		Logger: loggerhelper.LogConfig{
			LogLevel: "INFO",
		},
	}
}
