package main

import (
	"flag"
	"fmt"
	"github.com/nebius/nebius-observability-agent-updater/internal/config"
	"github.com/nebius/nebius-observability-agent-updater/internal/storage"
	"log"
)

func main() {
	// hello world
	fmt.Printf("Hello, world.\n")
	configPath := flag.String("config", "", "path to config file")
	flag.Parse()

	config, err := config.Load(*configPath)
	if err != nil {
		log.Fatal("failed to load config: ", err)
	}
	stateStorage := storage.NewDiskStorage(config.StatePath)
}
