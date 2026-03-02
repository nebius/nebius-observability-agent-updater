package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"
	"github.com/nebius/nebius-observability-agent-updater/internal/agents"
	"github.com/nebius/nebius-observability-agent-updater/internal/config"
	"github.com/nebius/nebius-observability-agent-updater/internal/healthcheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/goleak"
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

func (m *MockAgentData) GetEnvironmentFilePath() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockAgentData) Restart() error {
	args := m.Called()
	return args.Error(0)
}

type MockOSHelper struct {
	mock.Mock
}

func (m *MockOSHelper) GetSystemUptime() (time.Duration, error) {
	args := m.Called()
	return args.Get(0).(time.Duration), args.Error(1)
}

func (m *MockOSHelper) GetServiceUptime(serviceName string) (time.Duration, error) {
	args := m.Called(serviceName)
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
				agent.On("GetEnvironmentFilePath").Return("")
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
				agent.On("GetEnvironmentFilePath").Return("")
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
				agent.On("GetEnvironmentFilePath").Return("")
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
	agent.On("GetEnvironmentFilePath").Return("")
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

func TestGenerateEnvironmentFileContent(t *testing.T) {
	header := "# Managed by agent updater. Variables are loaded as env vars at agent startup.\n"

	tests := []struct {
		name     string
		flags    map[string]string
		expected string
	}{
		{
			name:     "nil flags",
			flags:    nil,
			expected: header,
		},
		{
			name:     "empty flags",
			flags:    map[string]string{},
			expected: header,
		},
		{
			name:  "single flag",
			flags: map[string]string{"FEATURE_FLAG_GPU_LOGS_COLLECTION_ENABLED": "true"},
			expected: header +
				"FEATURE_FLAG_GPU_LOGS_COLLECTION_ENABLED=true\n",
		},
		{
			name: "multiple flags sorted",
			flags: map[string]string{
				"FEATURE_FLAG_Z_LAST":  "false",
				"FEATURE_FLAG_A_FIRST": "true",
			},
			expected: header +
				"FEATURE_FLAG_A_FIRST=true\n" +
				"FEATURE_FLAG_Z_LAST=false\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateEnvironmentFileContent(tt.flags)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestApp_processFeatureFlags(t *testing.T) {
	header := "# Managed by agent updater. Variables are loaded as env vars at agent startup.\n"

	t.Run("empty env path skips processing", func(t *testing.T) {
		agent := &MockAgentData{}
		oh := &MockOSHelper{}
		agent.On("GetEnvironmentFilePath").Return("")

		app := &App{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), config: config.GetDefaultConfig(), oh: oh}
		restarted := app.processFeatureFlags(&agentmanager.GetVersionResponse{
			FeatureFlags: map[string]string{"FLAG": "true"},
		}, agent)

		assert.False(t, restarted)
		agent.AssertExpectations(t)
		oh.AssertExpectations(t)
	})

	t.Run("write new feature flags file and restart", func(t *testing.T) {
		agent := &MockAgentData{}
		oh := &MockOSHelper{}
		envPath := t.TempDir() + "/environment"

		agent.On("GetEnvironmentFilePath").Return(envPath)
		agent.On("GetServiceName").Return("test-agent")
		oh.On("GetServiceUptime", "test-agent").Return(20*time.Minute, nil)
		oh.On("GetSystemUptime").Return(1*time.Hour, nil)
		agent.On("Restart").Return(nil)

		app := &App{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), config: config.GetDefaultConfig(), oh: oh}
		restarted := app.processFeatureFlags(&agentmanager.GetVersionResponse{
			FeatureFlags: map[string]string{"FEATURE_FLAG_GPU_LOGS_COLLECTION_ENABLED": "true"},
		}, agent)

		assert.True(t, restarted)
		content, err := os.ReadFile(envPath)
		assert.NoError(t, err)
		assert.Equal(t, header+"FEATURE_FLAG_GPU_LOGS_COLLECTION_ENABLED=true\n", string(content))
		agent.AssertExpectations(t)
		oh.AssertExpectations(t)
	})

	t.Run("no restart when agent uptime less than 15 minutes", func(t *testing.T) {
		agent := &MockAgentData{}
		oh := &MockOSHelper{}
		envPath := t.TempDir() + "/environment"

		agent.On("GetEnvironmentFilePath").Return(envPath)
		agent.On("GetServiceName").Return("test-agent")
		oh.On("GetServiceUptime", "test-agent").Return(5*time.Minute, nil)
		oh.On("GetSystemUptime").Return(1*time.Hour, nil)

		app := &App{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), config: config.GetDefaultConfig(), oh: oh}
		restarted := app.processFeatureFlags(&agentmanager.GetVersionResponse{
			FeatureFlags: map[string]string{"FLAG": "true"},
		}, agent)

		assert.False(t, restarted)
		content, err := os.ReadFile(envPath)
		assert.NoError(t, err)
		assert.Equal(t, header+"FLAG=true\n", string(content))
		agent.AssertNotCalled(t, "Restart")
		oh.AssertExpectations(t)
	})

	t.Run("no restart when content unchanged and file older than agent", func(t *testing.T) {
		agent := &MockAgentData{}
		oh := &MockOSHelper{}
		envPath := t.TempDir() + "/environment"

		content := header + "FLAG=true\n"
		err := os.WriteFile(envPath, []byte(content), 0640)
		assert.NoError(t, err)
		oldTime := time.Now().Add(-1 * time.Hour)
		err = os.Chtimes(envPath, oldTime, oldTime)
		assert.NoError(t, err)

		agent.On("GetEnvironmentFilePath").Return(envPath)
		agent.On("GetServiceName").Return("test-agent")
		oh.On("GetServiceUptime", "test-agent").Return(30*time.Minute, nil)
		oh.On("GetSystemUptime").Return(2*time.Hour, nil)

		app := &App{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), config: config.GetDefaultConfig(), oh: oh}
		restarted := app.processFeatureFlags(&agentmanager.GetVersionResponse{
			FeatureFlags: map[string]string{"FLAG": "true"},
		}, agent)

		assert.False(t, restarted)
		agent.AssertNotCalled(t, "Restart")
		oh.AssertExpectations(t)
	})

	t.Run("no spurious restart when file mtime within grace period of agent start", func(t *testing.T) {
		agent := &MockAgentData{}
		oh := &MockOSHelper{}
		envPath := t.TempDir() + "/environment"

		content := header + "FLAG=true\n"
		err := os.WriteFile(envPath, []byte(content), 0640)
		assert.NoError(t, err)
		err = os.Chtimes(envPath, time.Now().Add(-46*time.Second), time.Now().Add(-46*time.Second))
		assert.NoError(t, err)

		agent.On("GetEnvironmentFilePath").Return(envPath)
		agent.On("GetServiceName").Return("test-agent")
		oh.On("GetServiceUptime", "test-agent").Return(45*time.Second, nil)
		oh.On("GetSystemUptime").Return(1*time.Hour, nil)

		app := &App{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), config: config.GetDefaultConfig(), oh: oh}
		restarted := app.processFeatureFlags(&agentmanager.GetVersionResponse{
			FeatureFlags: map[string]string{"FLAG": "true"},
		}, agent)

		assert.False(t, restarted)
		agent.AssertNotCalled(t, "Restart")
		oh.AssertExpectations(t)
	})

	t.Run("fresh boot restarts agent immediately even with low uptime", func(t *testing.T) {
		agent := &MockAgentData{}
		oh := &MockOSHelper{}
		envPath := t.TempDir() + "/environment"

		agent.On("GetEnvironmentFilePath").Return(envPath)
		agent.On("GetServiceName").Return("test-agent")
		oh.On("GetServiceUptime", "test-agent").Return(13*time.Minute+54*time.Second, nil)
		oh.On("GetSystemUptime").Return(14*time.Minute+21*time.Second, nil)
		agent.On("Restart").Return(nil)

		app := &App{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), config: config.GetDefaultConfig(), oh: oh}
		restarted := app.processFeatureFlags(&agentmanager.GetVersionResponse{
			FeatureFlags: map[string]string{"FLAG": "true"},
		}, agent)

		assert.True(t, restarted)
		content, err := os.ReadFile(envPath)
		assert.NoError(t, err)
		assert.Equal(t, header+"FLAG=true\n", string(content))
		agent.AssertExpectations(t)
		oh.AssertExpectations(t)
	})

	t.Run("pending restart after previous crash", func(t *testing.T) {
		agent := &MockAgentData{}
		oh := &MockOSHelper{}
		envPath := t.TempDir() + "/environment"

		content := header + "FLAG=true\n"
		err := os.WriteFile(envPath, []byte(content), 0640)
		assert.NoError(t, err)
		err = os.Chtimes(envPath, time.Now().Add(-5*time.Minute), time.Now().Add(-5*time.Minute))
		assert.NoError(t, err)

		agent.On("GetEnvironmentFilePath").Return(envPath)
		agent.On("GetServiceName").Return("test-agent")
		oh.On("GetServiceUptime", "test-agent").Return(20*time.Minute, nil)
		oh.On("GetSystemUptime").Return(1*time.Hour, nil)
		agent.On("Restart").Return(nil)

		app := &App{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), config: config.GetDefaultConfig(), oh: oh}
		restarted := app.processFeatureFlags(&agentmanager.GetVersionResponse{
			FeatureFlags: map[string]string{"FLAG": "true"},
		}, agent)

		assert.True(t, restarted)
		agent.AssertExpectations(t)
		oh.AssertExpectations(t)
	})

	t.Run("pending restart deferred when agent uptime less than 15 minutes", func(t *testing.T) {
		agent := &MockAgentData{}
		oh := &MockOSHelper{}
		envPath := t.TempDir() + "/environment"

		content := header + "FLAG=true\n"
		err := os.WriteFile(envPath, []byte(content), 0640)
		assert.NoError(t, err)
		tenMinAgo := time.Now().Add(-10 * time.Minute)
		err = os.Chtimes(envPath, tenMinAgo, tenMinAgo)
		assert.NoError(t, err)

		agent.On("GetEnvironmentFilePath").Return(envPath)
		agent.On("GetServiceName").Return("test-agent")
		oh.On("GetServiceUptime", "test-agent").Return(5*time.Minute, nil)
		oh.On("GetSystemUptime").Return(1*time.Hour, nil)

		app := &App{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), config: config.GetDefaultConfig(), oh: oh}
		restarted := app.processFeatureFlags(&agentmanager.GetVersionResponse{
			FeatureFlags: map[string]string{"FLAG": "true"},
		}, agent)

		assert.False(t, restarted)
		agent.AssertNotCalled(t, "Restart")
		oh.AssertExpectations(t)
	})
}

func TestApp_validateFeatureFlags(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := &App{logger: logger}

	tests := []struct {
		name     string
		input    map[string]string
		expected map[string]string
	}{
		{
			name:     "nil flags",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty flags",
			input:    map[string]string{},
			expected: map[string]string{},
		},
		{
			name:     "valid keys",
			input:    map[string]string{"FEATURE_FLAG": "true", "_PRIVATE": "1", "a": "b"},
			expected: map[string]string{"FEATURE_FLAG": "true", "_PRIVATE": "1", "a": "b"},
		},
		{
			name:     "invalid key with space",
			input:    map[string]string{"BAD KEY": "true", "GOOD_KEY": "val"},
			expected: map[string]string{"GOOD_KEY": "val"},
		},
		{
			name:     "invalid key starting with digit",
			input:    map[string]string{"1BAD": "true"},
			expected: map[string]string{},
		},
		{
			name:     "invalid key with special chars",
			input:    map[string]string{"BAD-KEY": "true", "BAD.KEY": "false"},
			expected: map[string]string{},
		},
		{
			name:     "newline in value",
			input:    map[string]string{"FLAG": "line1\nline2"},
			expected: map[string]string{},
		},
		{
			name:     "carriage return in value",
			input:    map[string]string{"FLAG": "line1\rline2"},
			expected: map[string]string{},
		},
		{
			name:     "mixed valid and invalid",
			input:    map[string]string{"GOOD": "ok", "BAD KEY": "no", "ALSO_GOOD": "yes", "NL_VAL": "a\nb"},
			expected: map[string]string{"GOOD": "ok", "ALSO_GOOD": "yes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := app.validateFeatureFlags(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestApp_poll_restart_with_feature_flag_change(t *testing.T) {
	client := &MockUpdaterClient{}
	agent := &MockAgentData{}
	oh := &MockOSHelper{}
	envPath := t.TempDir() + "/environment"

	client.On("SendAgentData", mock.Anything).Return(&agentmanager.GetVersionResponse{
		Action:       agentmanager.Action_RESTART,
		FeatureFlags: map[string]string{"NEW_FLAG": "true"},
	}, nil)
	agent.On("GetServiceName").Return("test-agent")
	agent.On("GetEnvironmentFilePath").Return(envPath)
	oh.On("GetServiceUptime", "test-agent").Return(20*time.Minute, nil)
	oh.On("GetSystemUptime").Return(1*time.Hour, nil)
	agent.On("Restart").Return(nil).Once()

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
}
