package application

import (
	"context"
	"github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"
	"github.com/nebius/nebius-observability-agent-updater/internal/agents"
	"github.com/nebius/nebius-observability-agent-updater/internal/config"
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

type App struct {
	config *config.Config
	client updaterClient
	logger *slog.Logger
	agents []agents.AgentData
}

type updaterClient interface {
	SendAgentData(agent agents.AgentData) (*agentmanager.GetVersionResponse, error)
	Close()
}

func New(config *config.Config, client updaterClient, logger *slog.Logger, agents []agents.AgentData) *App {
	app := &App{config: config, client: client, logger: logger, agents: agents}
	return app
}

func (s *App) poll(agent agents.AgentData) {
	s.logger.Info("Polling for ", "agent", agent.GetServiceName())
	response, err := s.client.SendAgentData(agent)
	if err != nil {
		s.logger.Error("Failed to send agent data", "error", err, "agent", agent.GetServiceName())
		return
	}
	s.logger.Debug("Received response", "response", response, "agent", agent.GetServiceName())
	if response.Action == agentmanager.Action_UPDATE {
		updateData := response.GetUpdate()
		if updateData == nil {
			s.logger.Error("Received empty update data")
			return
		}
		s.logger.Info("Updating agent to version", "version", updateData.GetVersion(), "agent", agent.GetServiceName())
		err := agent.Update(s.config.UpdateRepoScriptPath, updateData.GetVersion())
		if err != nil {
			s.logger.Error("Failed to update agent", "error", err)
			return
		}
	}
	if response.Action == agentmanager.Action_RESTART {
		s.logger.Info("Restarting agent after service command", "agent", agent.GetServiceName())
		err := agent.Restart()
		if err != nil {
			s.logger.Error("Failed to restart agent", "error", err)
			return
		}
	}
}

func (s *App) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for _, agent := range s.agents {
		wg.Add(1)
		go func(a agents.AgentData) {
			defer wg.Done()
			s.runForAgent(ctx, a)
		}(agent)
	}
	wg.Wait()
	return nil
}

func (s *App) runForAgent(ctx context.Context, agent agents.AgentData) {
	for {
		interval := s.config.PollInterval + time.Duration(float64(s.config.PollJitter)*(2*rand.Float64()-1))
		s.logger.Info("Calculated poll interval", "poll_interval", interval.String(), "agent", agent.GetServiceName())
		if interval < 0 {
			interval = 0
		}
		select {
		case <-time.After(interval):
			s.poll(agent)
		case <-ctx.Done():
			return
		}
	}
}

func (s *App) Shutdown() error {
	return nil
}
