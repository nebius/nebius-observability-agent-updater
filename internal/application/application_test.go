package application

import (
	"context"
	"errors"
	"github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"
	"github.com/nebius/nebius-observability-agent-updater/internal/healthcheck"
	"io"
	"testing"
	"time"

	"github.com/nebius/nebius-observability-agent-updater/internal/agents"
	"github.com/nebius/nebius-observability-agent-updater/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/goleak"
	"log/slog"
)

type MockUpdaterClient struct {
	mock.Mock
}

func (m *MockUpdaterClient) SendAgentData(agent agents.AgentData) (*agentmanager.GetVersionResponse, error) {
	args := m.Called(agent)
	return args.Get(0).(*agentmanager.GetVersionResponse), args.Error(1)
}

func (m *MockUpdaterClient) Close() {
	m.Called()
}

type MockAgentData struct {
	mock.Mock
}

func (m *MockAgentData) GetServiceName() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockAgentData) Update(updateScriptPath string, version string) error {
	args := m.Called(updateScriptPath, version)
	return args.Error(0)
}

func (m *MockAgentData) GetAgentType() agentmanager.AgentType {
	return agentmanager.AgentType_O11Y_AGENT
}

func (m *MockAgentData) GetDebPackageName() string {
	return "nebius-observability-agent"
}

func (m *MockAgentData) GetHealthCheckUrl() string {
	return "http://localhost:8080/health"
}
func (m *MockAgentData) GetSystemdServiceName() string {
	return "nebius-observability-agent"
}
func (m *MockAgentData) IsAgentHealthy() (bool, healthcheck.Response) {
	return true, healthcheck.Response{}
}

func (m *MockAgentData) GetLastUpdateError() error {
	return nil
}

func (m *MockAgentData) Restart() error {
	args := m.Called()
	return args.Error(0)
}

// New mock for OSHelper
type MockOSHelper struct {
	mock.Mock
}

func (m *MockOSHelper) GetSystemUptime() (time.Duration, error) {
	args := m.Called()
	return args.Get(0).(time.Duration), args.Error(1)
}

func TestApp_New(t *testing.T) {
	cfg := &config.Config{}
	client := &MockUpdaterClient{}
	logger := slog.Default()
	agents := []agents.AgentData{&MockAgentData{}}
	oh := &MockOSHelper{}

	app := New(cfg, client, logger, agents, oh)

	assert.NotNil(t, app)
	assert.Equal(t, cfg, app.config)
	assert.Equal(t, client, app.client)
	assert.Equal(t, logger, app.logger)
	assert.Equal(t, agents, app.agents)
	assert.Equal(t, oh, app.oh)
}

func TestApp_poll(t *testing.T) {
	tests := []struct {
		name           string
		setupMocks     func(*MockUpdaterClient, *MockAgentData, *MockOSHelper)
		expectedLogMsg string
	}{
		{
			name: "Successful poll with no update",
			setupMocks: func(client *MockUpdaterClient, agent *MockAgentData, oh *MockOSHelper) {
				client.On("SendAgentData", mock.Anything).Return(&agentmanager.GetVersionResponse{Action: agentmanager.Action_NOP}, nil)
				agent.On("GetServiceName").Return("test-agent")
			},
			expectedLogMsg: "Polling for ",
		},
		{
			name: "Successful poll with update",
			setupMocks: func(client *MockUpdaterClient, agent *MockAgentData, oh *MockOSHelper) {
				client.On("SendAgentData", mock.Anything).Return(&agentmanager.GetVersionResponse{
					Action:   agentmanager.Action_UPDATE,
					Response: &agentmanager.GetVersionResponse_Update{Update: &agentmanager.UpdateActionParams{Version: "1.0.1"}},
				}, nil)
				agent.On("GetServiceName").Return("test-agent")
				oh.On("GetSystemUptime").Return(20*time.Minute, nil)
				agent.On("Update", mock.Anything, "1.0.1").Return(nil)
			},
			expectedLogMsg: "Polling for ",
		},
		{
			name: "Successful poll with restart",
			setupMocks: func(client *MockUpdaterClient, agent *MockAgentData, oh *MockOSHelper) {
				client.On("SendAgentData", mock.Anything).Return(&agentmanager.GetVersionResponse{
					Action: agentmanager.Action_RESTART,
				}, nil)
				agent.On("GetServiceName").Return("test-agent")
				agent.On("Restart").Return(nil)
			},
			expectedLogMsg: "Polling for ",
		},
		{
			name: "Failed to send agent data",
			setupMocks: func(client *MockUpdaterClient, agent *MockAgentData, oh *MockOSHelper) {
				client.On("SendAgentData", mock.Anything).Return((*agentmanager.GetVersionResponse)(nil), errors.New("network error"))
				agent.On("GetServiceName").Return("test-agent")
			},
			expectedLogMsg: "Failed to send agent data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockUpdaterClient{}
			agent := &MockAgentData{}
			oh := &MockOSHelper{}
			tt.setupMocks(client, agent, oh)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			app := &App{
				client: client,
				logger: logger,
				config: config.GetDefaultConfig(),
				oh:     oh,
			}

			app.poll(agent)

			client.AssertExpectations(t)
			agent.AssertExpectations(t)
			oh.AssertExpectations(t)
		})
	}
}

func TestApp_Update(t *testing.T) {
	tests := []struct {
		name         string
		setupMocks   func(*MockAgentData, *MockOSHelper)
		response     *agentmanager.GetVersionResponse
		shouldUpdate bool
	}{
		{
			name: "Update with sufficient uptime",
			setupMocks: func(agent *MockAgentData, oh *MockOSHelper) {
				oh.On("GetSystemUptime").Return(20*time.Minute, nil)
				agent.On("GetServiceName").Return("test-agent")
				agent.On("Update", mock.Anything, "1.0.1").Return(nil)
			},
			response: &agentmanager.GetVersionResponse{
				Action:   agentmanager.Action_UPDATE,
				Response: &agentmanager.GetVersionResponse_Update{Update: &agentmanager.UpdateActionParams{Version: "1.0.1"}},
			},
			shouldUpdate: true,
		},
		{
			name: "Skip update with insufficient uptime",
			setupMocks: func(agent *MockAgentData, oh *MockOSHelper) {
				oh.On("GetSystemUptime").Return(10*time.Minute, nil)
			},
			response: &agentmanager.GetVersionResponse{
				Action:   agentmanager.Action_UPDATE,
				Response: &agentmanager.GetVersionResponse_Update{Update: &agentmanager.UpdateActionParams{Version: "1.0.1"}},
			},
			shouldUpdate: false,
		},
		{
			name: "Update with error getting uptime",
			setupMocks: func(agent *MockAgentData, oh *MockOSHelper) {
				oh.On("GetSystemUptime").Return(time.Duration(0), errors.New("uptime error"))
				agent.On("GetServiceName").Return("test-agent")
				agent.On("Update", mock.Anything, "1.0.1").Return(nil)
			},
			response: &agentmanager.GetVersionResponse{
				Action:   agentmanager.Action_UPDATE,
				Response: &agentmanager.GetVersionResponse_Update{Update: &agentmanager.UpdateActionParams{Version: "1.0.1"}},
			},
			shouldUpdate: true,
		},
		{
			name: "Update with empty update data",
			setupMocks: func(agent *MockAgentData, oh *MockOSHelper) {
				oh.On("GetSystemUptime").Return(20*time.Minute, nil)
			},
			response: &agentmanager.GetVersionResponse{
				Action: agentmanager.Action_UPDATE,
			},
			shouldUpdate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &MockAgentData{}
			oh := &MockOSHelper{}
			tt.setupMocks(agent, oh)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			app := &App{
				logger: logger,
				config: config.GetDefaultConfig(),
				oh:     oh,
			}

			app.Update(tt.response, agent)

			agent.AssertExpectations(t)
			oh.AssertExpectations(t)
		})
	}
}

func TestApp_Restart(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func(*MockAgentData)
		shouldRestart bool
	}{
		{
			name: "Successful restart",
			setupMocks: func(agent *MockAgentData) {
				agent.On("GetServiceName").Return("test-agent")
				agent.On("Restart").Return(nil)
			},
			shouldRestart: true,
		},
		{
			name: "Failed restart",
			setupMocks: func(agent *MockAgentData) {
				agent.On("GetServiceName").Return("test-agent")
				agent.On("Restart").Return(errors.New("restart error"))
			},
			shouldRestart: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &MockAgentData{}
			tt.setupMocks(agent)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			app := &App{
				logger: logger,
				config: config.GetDefaultConfig(),
			}

			app.Restart(agent)

			agent.AssertExpectations(t)
		})
	}
}

func TestApp_Run(t *testing.T) {
	defer goleak.VerifyNone(t)

	cfg := &config.Config{
		PollInterval: 10 * time.Millisecond,
		PollJitter:   5 * time.Millisecond,
	}
	client := &MockUpdaterClient{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	agent := &MockAgentData{}
	oh := &MockOSHelper{}

	agent.On("GetServiceName").Return("test-agent")
	client.On("SendAgentData", mock.Anything).Return(&agentmanager.GetVersionResponse{Action: agentmanager.Action_NOP}, nil)

	app := New(cfg, client, logger, []agents.AgentData{agent}, oh)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := app.Run(ctx)

	assert.NoError(t, err)
	client.AssertExpectations(t)
	agent.AssertExpectations(t)
}

func TestApp_Shutdown(t *testing.T) {
	app := &App{}
	err := app.Shutdown()
	assert.NoError(t, err)
}
