package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
)

func Load(path string) (*Config, error) {
	config := GetDefaultConfig()
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer func() { _ = f.Close() }()

	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(config)
	if err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}
	return config, nil
}
