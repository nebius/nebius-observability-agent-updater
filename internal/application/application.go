package application

import (
	"context"
	generated "github.com/nebius/nebius-observability-agent-updater/generated/proto"
	"github.com/nebius/nebius-observability-agent-updater/internal/agents"
	"github.com/nebius/nebius-observability-agent-updater/internal/config"
	"log/slog"
	"math/rand"
	"time"
)

type App struct {
	config *config.Config
	state  stateStorage
	states map[generated.AppState]func(context.Context) generated.AppState
	client updaterClient
	logger *slog.Logger
}

type stateStorage interface {
	SaveState(*generated.StorageState) error
	LoadState() (*generated.StorageState, error)
}

type updaterClient interface {
	SendAgentData(agent agents.AgentData, isAgentHealthy bool) (*generated.GetVersionResponse, error)
	Close()
}

func New(config *config.Config, state stateStorage, client updaterClient, logger *slog.Logger) *App {
	app := &App{config: config, client: client, state: state, logger: logger}
	app.states = map[generated.AppState]func(context.Context) generated.AppState{
		generated.AppState_NORMAL:   app.normal,
		generated.AppState_UPDATING: app.updating,
	}
	return app
}

func (s *App) normal(context context.Context) generated.AppState {
	interval := s.config.PollInterval + time.Duration(float64(s.config.PollJitter)*(2*rand.Float64()-1))
	s.logger.Info("Switching to normal state", "poll_interval", interval.String())
	if interval < 0 {
		interval = 0
	}
	select {
	case <-time.After(interval):
		if s.Poll() {
			return generated.AppState_UPDATING
		}
	case <-context.Done():
		return generated.AppState_SHUTDOWN
	}
	return generated.AppState_NORMAL
}

func (s *App) Poll() bool {
	s.logger.Info("Polling")
	agentData := agents.NewO11yagent()
	_, err := s.client.SendAgentData(agentData, true)
	if err != nil {
		s.logger.Error("Failed to send agent data", "error", err)
	}
	return true
}

func (s *App) updating(_ context.Context) generated.AppState {
	s.logger.Info("Switching to updating state")
	return generated.AppState_NORMAL
}

func (s *App) Run(ctx context.Context) error {
	state, err := s.state.LoadState()
	if err != nil {
		s.logger.Error("Failed to load state", "error", err)
		state = &generated.StorageState{}
	}
	currentState := state.AppState
	for {
		nextState := s.states[currentState](ctx)
		if nextState == generated.AppState_SHUTDOWN {
			return s.Shutdown(ctx)
		}
		if nextState != currentState {
			state.AppState = nextState
			err := s.state.SaveState(state)
			if err != nil {
				s.logger.Error("Failed to save state", "error", err)
			}
			currentState = nextState
		}
	}
}

func (s *App) Shutdown(_ context.Context) error {
	return nil
}
