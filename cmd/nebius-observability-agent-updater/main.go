package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/nebius/nebius-observability-agent-updater/internal/application"
	"github.com/nebius/nebius-observability-agent-updater/internal/client"
	"github.com/nebius/nebius-observability-agent-updater/internal/config"
	"github.com/nebius/nebius-observability-agent-updater/internal/loggerhelper"
	"github.com/nebius/nebius-observability-agent-updater/internal/metadata"
	"github.com/nebius/nebius-observability-agent-updater/internal/osutils"
	"github.com/nebius/nebius-observability-agent-updater/internal/storage"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configPath := flag.String("config", "", "path to config file")
	flag.Parse()
	var cfg *config.Config
	var err error
	if *configPath == "" {
		cfg = config.GetDefaultConfig()
	} else {
		cfg, err = config.Load(*configPath)
		if err != nil {
			log.Fatal("failed to load config: ", err)
		}
	}
	logger := loggerhelper.InitLogger(&cfg.Logger)
	stateStorage := storage.NewDiskStorage(cfg.StatePath, logger)
	metadataReader := metadata.NewReader(cfg.Metadata, logger)
	oh := osutils.NewOsHelper()
	cli, err := client.New(metadataReader, oh, &cfg.GRPC, logger)
	if err != nil {
		log.Fatal("failed to create client: ", err)
	}

	app := application.New(cfg, stateStorage, cli, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Run the app in a separate goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- app.Run(ctx)
	}()

	exitCode := 0
	select {
	case err := <-errChan:
		if err != nil {
			fmt.Printf("App exited with error: %v\n", err)
			exitCode = 1
		} else {
			fmt.Println("App exited successfully")
		}
	case sig := <-sigChan:
		fmt.Printf("\nReceived signal: %v. Cancelling context...\n", sig)
		cancel()         // Cancel the context
		err := <-errChan // Wait for the app to finish
		if err != nil {
			fmt.Printf("App exited with error: %v\n", err)
			exitCode = 1
		} else {
			fmt.Println("App shut down gracefully")
		}
	}
	defer os.Exit(exitCode)
}
