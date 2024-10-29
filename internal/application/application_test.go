package application

import (
	"context"
	"errors"
	generated "github.com/nebius/nebius-observability-agent-updater/generated/proto"
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

func (m *MockUpdaterClient) SendAgentData(agent agents.AgentData) (*generated.GetVersionResponse, error) {
	args := m.Called(agent)
	return args.Get(0).(*generated.GetVersionResponse), args.Error(1)
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
	args := m.Called(version)
	return args.Error(0)
}

func (m *MockAgentData) GetAgentType() generated.AgentType {
	return generated.AgentType_O11Y_AGENT
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
func (m *MockAgentData) IsAgentHealthy() (bool, []string) {
	return true, nil
}

func (m *MockAgentData) GetLastUpdateError() error {
	return nil
}

func (m *MockAgentData) Restart() error {
	args := m.Called()
	return args.Error(0)
}
func TestApp_New(t *testing.T) {
	cfg := &config.Config{}
	client := &MockUpdaterClient{}
	logger := slog.Default()
	agents := []agents.AgentData{&MockAgentData{}}

	app := New(cfg, client, logger, agents)

	assert.NotNil(t, app)
	assert.Equal(t, cfg, app.config)
	assert.Equal(t, client, app.client)
	assert.Equal(t, logger, app.logger)
	assert.Equal(t, agents, app.agents)
}

func TestApp_poll(t *testing.T) {
	tests := []struct {
		name           string
		setupMocks     func(*MockUpdaterClient, *MockAgentData)
		expectedLogMsg string
	}{
		{
			name: "Successful poll with no update",
			setupMocks: func(client *MockUpdaterClient, agent *MockAgentData) {
				client.On("SendAgentData", mock.Anything).Return(&generated.GetVersionResponse{Action: generated.Action_NOP}, nil)
				agent.On("GetServiceName").Return("test-agent")
			},
			expectedLogMsg: "Polling for ",
		},
		{
			name: "Successful poll with update",
			setupMocks: func(client *MockUpdaterClient, agent *MockAgentData) {
				client.On("SendAgentData", mock.Anything).Return(&generated.GetVersionResponse{
					Action:   generated.Action_UPDATE,
					Response: &generated.GetVersionResponse_Update{Update: &generated.UpdateActionParams{Version: "1.0.1"}},
				}, nil)
				agent.On("GetServiceName").Return("test-agent")
				agent.On("Update", "1.0.1").Return(nil)
			},
			expectedLogMsg: "Polling for ",
		},
		{
			name: "Successful poll with restart",
			setupMocks: func(client *MockUpdaterClient, agent *MockAgentData) {
				client.On("SendAgentData", mock.Anything).Return(&generated.GetVersionResponse{
					Action: generated.Action_RESTART,
				}, nil)
				agent.On("GetServiceName").Return("test-agent")
				agent.On("Restart").Return(nil)
			},
			expectedLogMsg: "Polling for ",
		},
		{
			name: "Failed to send agent data",
			setupMocks: func(client *MockUpdaterClient, agent *MockAgentData) {
				client.On("SendAgentData", mock.Anything).Return((*generated.GetVersionResponse)(nil), errors.New("network error"))
				agent.On("GetServiceName").Return("test-agent")
			},
			expectedLogMsg: "Failed to send agent data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockUpdaterClient{}
			agent := &MockAgentData{}
			tt.setupMocks(client, agent)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			app := &App{
				client: client,
				logger: logger,
				config: config.GetDefaultConfig(),
			}

			app.poll(agent)

			client.AssertExpectations(t)
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

	agent.On("GetServiceName").Return("test-agent")
	client.On("SendAgentData", mock.Anything).Return(&generated.GetVersionResponse{Action: generated.Action_NOP}, nil)

	app := New(cfg, client, logger, []agents.AgentData{agent})

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
