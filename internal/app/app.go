package app

import (
	"context"
	"fmt"
	generated "github.com/nebius/nebius-observability-agent-updater/generated/proto"
	"github.com/nebius/nebius-observability-agent-updater/internal/config"
	"math"
	"math/rand"
	"time"
)

type App struct {
	config *config.Config
	state  stateStorage
	states map[generated.AppState]func(context.Context) generated.AppState
}

type stateStorage interface {
	SaveState(*generated.StorageState) error
	LoadState() (*generated.StorageState, error)
}

func New(config *config.Config, state stateStorage) *App {
	app := &App{config: config}
	app.states = map[generated.AppState]func(context.Context) generated.AppState{
		generated.AppState_NORMAL:   app.normal,
		generated.AppState_UPDATING: app.updating,
	}
	return app
}

func (s *App) normal(context context.Context) generated.AppState {
	interval := s.config.PollInterval + time.Duration(float64(s.config.PollJitter)*(2*rand.Float64()-1))
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

}
func (s *App) updating(context context.Context) generated.AppState {
	return generated.AppState_NORMAL
}

func (s *App) Run(ctx context.Context) error {
	state, err := s.state.LoadState()
	if err != nil {
		//FIXME metric and log
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
				//FIXME metric and log
			}
			currentState = nextState
		}
	}
}

func (s *App) Shutdown(_ context.Context) error {
	return nil
}

func (s *App) jitterSleep(ctx context.Context) bool {
	sleepDuration := time.Duration(float64(s.config.PollJitter) * (2*rand.Float64() - 1))
	sleepDuration = time.Duration(math.Max(float64(sleepDuration), 0))

	fmt.Printf("Sleeping for additional %.2f seconds\n", sleepDuration.Seconds())

	timer := time.NewTimer(sleepDuration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
