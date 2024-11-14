package clientconfig

import "time"

type KeepAliveConfig struct {
	Time                time.Duration `yaml:"time"`
	Timeout             time.Duration `yaml:"timeout"`
	PermitWithoutStream bool          `yaml:"permit_without_stream"`
}

type GRPCConfig struct {
	Endpoint  string          `yaml:"endpoint"`
	TLS       TLSConfig       `yaml:"tls"`
	Insecure  bool            `yaml:"insecure"`
	Timeout   time.Duration   `yaml:"timeout"`
	Retry     RetryConfig     `yaml:"retry"`
	KeepAlive KeepAliveConfig `yaml:"keep_alive"`
}

func GetDefaultGrpcConfig() GRPCConfig {
	return GRPCConfig{
		Endpoint: "observability-agent-manager.eu-north1.nebius.cloud:443",
		Insecure: false,
		Timeout:  5 * time.Second,
		Retry:    GetDefaultRetryConfig(),
		KeepAlive: KeepAliveConfig{
			Time:                20 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		},
	}
}

type RetryConfig struct {
	Enabled             bool          `yaml:"enabled"`
	MaxElapsedTime      time.Duration `yaml:"max_elapsed_time"`
	InitialInterval     time.Duration `yaml:"initial_interval"`
	Multiplier          float64       `yaml:"multiplier"`
	RandomizationFactor float64       `yaml:"randomization_factor"`
}

func GetDefaultRetryConfig() RetryConfig {
	return RetryConfig{
		Enabled:             false,
		MaxElapsedTime:      time.Second * 30,
		InitialInterval:     time.Second,
		Multiplier:          1.5,
		RandomizationFactor: 0.5,
	}
}

type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	CAFile   string `yaml:"ca_file"`
}
