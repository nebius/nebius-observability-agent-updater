package application

import (
	"context"
	generated "github.com/nebius/nebius-observability-agent-updater/generated/proto"
	"github.com/nebius/nebius-observability-agent-updater/internal/agents"
	"github.com/nebius/nebius-observability-agent-updater/internal/config"
	"google.golang.org/protobuf/types/known/timestamppb"
	"log/slog"
	"math/rand"
	"time"
)

type App struct {
	config  *config.Config
	stateSt stateStorage
	state   *generated.StorageState
	states  map[generated.AppState]func(context.Context) generated.AppState
	client  updaterClient
	logger  *slog.Logger
}

type stateStorage interface {
	SaveState(*generated.StorageState) error
	LoadState() (*generated.StorageState, error)
}

type updaterClient interface {
	SendAgentData(agent agents.AgentData) (*generated.GetVersionResponse, error)
	Close()
}

func New(config *config.Config, state stateStorage, client updaterClient, logger *slog.Logger) *App {
	app := &App{config: config, client: client, stateSt: state, logger: logger}
	app.states = map[generated.AppState]func(context.Context) generated.AppState{
		generated.AppState_NORMAL:   app.normal,
		generated.AppState_UPDATING: app.updating,
	}
	return app
}

func (s *App) normal(context context.Context) generated.AppState {
	interval := s.config.PollInterval + time.Duration(float64(s.config.PollJitter)*(2*rand.Float64()-1))
	if time.Since(s.state.LastUpdated.AsTime()) > s.config.PollInterval {
		interval = 0
	}
	s.logger.Info("Switching to normal stateSt", "poll_interval", interval.String())
	if interval < 0 {
		interval = 0
	}
	select {
	case <-time.After(interval):
		if s.Poll() {
			return generated.AppState_UPDATING
		}
	case <-context.Done():
		return generated.AppState_NORMAL
	}
	return generated.AppState_NORMAL
}

func (s *App) Poll() bool {
	s.logger.Info("Polling")
	agentData := agents.NewO11yagent()
	now := timestamppb.Now()
	s.state.LastUpdated = now
	if err := s.stateSt.SaveState(s.state); err != nil {
		s.logger.Error("Failed to save stateSt", "error", err)
	}
	_, err := s.client.SendAgentData(agentData)
	if err != nil {
		s.logger.Error("Failed to send agent data", "error", err)
		return false
	}
	return true
}

func (s *App) calculateStartState() generated.AppState {
	if s.state.NewVersion != "" && s.state.NewVersion != s.state.StableVersion {
		return generated.AppState_UPDATING
	}
	return generated.AppState_NORMAL
}

func (s *App) updating(_ context.Context) generated.AppState {
	s.logger.Info("Switching to updating stateSt")
	return generated.AppState_NORMAL
}

func (s *App) Run(ctx context.Context) error {
	var err error
	s.state, err = s.stateSt.LoadState()
	if err != nil {
		s.logger.Error("Failed to load stateSt", "error", err)
		s.state = &generated.StorageState{}
	}
	currentState := s.calculateStartState()
	for {
		nextState := s.states[currentState](ctx)
		select {
		case <-ctx.Done():
			return s.Shutdown()
		default:
		}
		if nextState != currentState {
			currentState = nextState
		}
	}
}

func (s *App) Shutdown() error {
	return nil
}
