package main

import (
	"context"
	"flag"
	"github.com/nebius/nebius-observability-agent-updater/internal/agents"
	"github.com/nebius/nebius-observability-agent-updater/internal/application"
	"github.com/nebius/nebius-observability-agent-updater/internal/client"
	"github.com/nebius/nebius-observability-agent-updater/internal/config"
	"github.com/nebius/nebius-observability-agent-updater/internal/loggerhelper"
	"github.com/nebius/nebius-observability-agent-updater/internal/metadata"
	"github.com/nebius/nebius-observability-agent-updater/internal/osutils"
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
	metadataReader := metadata.NewReader(cfg.Metadata, logger)
	oh := osutils.NewOsHelper()
	cli, err := client.New(metadataReader, oh, &cfg.GRPC, logger, metadataReader.GetIamToken)
	if err != nil {
		logger.Error("failed to create client", "error", err)
		defer syscall.Exit(1)
		return
	}
	agentsList := []agents.AgentData{agents.NewO11yagent()}
	app := application.New(cfg, cli, logger, agentsList)
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
			logger.Error("App exited with error", "error", err)
			exitCode = 1
		} else {
			logger.Info("App shut down gracefully")
		}
	case sig := <-sigChan:
		logger.Info("Received signal, cancelling context", "signal", sig)
		cancel()         // Cancel the context
		err := <-errChan // Wait for the app to finish
		if err != nil {
			logger.Error("App exited with error", "error", err)
			exitCode = 1
		} else {
			logger.Info("App shut down gracefully")
		}
	}
	defer os.Exit(exitCode)
}
