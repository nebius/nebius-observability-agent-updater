package client

import (
	"context"
	"fmt"
	"github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"
	"github.com/nebius/nebius-observability-agent-updater/internal/client/clientconfig"
	"github.com/nebius/nebius-observability-agent-updater/internal/config"
	"github.com/nebius/nebius-observability-agent-updater/internal/healthcheck"
	"github.com/nebius/nebius-observability-agent-updater/internal/osutils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
)

func tokenFunc() (string, error) {
	return "token", nil
}

// Mock implementations
type mockMetadataReader struct {
	mock.Mock
}

func (m *mockMetadataReader) GetParentId() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *mockMetadataReader) GetInstanceId() (string, bool, error) {
	args := m.Called()
	return args.String(0), false, args.Error(1)
}

func (m *mockMetadataReader) GetIamToken() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

type mockOSHelper struct {
	mock.Mock
}

func (m *mockOSHelper) GetDebVersion(packageName string) (string, error) {
	args := m.Called(packageName)
	return args.String(0), args.Error(1)
}

func (m *mockOSHelper) GetServiceUptime(serviceName string) (time.Duration, error) {
	args := m.Called(serviceName)
	return args.Get(0).(time.Duration), args.Error(1)
}

func (m *mockOSHelper) GetSystemUptime() (time.Duration, error) {
	args := m.Called()
	return args.Get(0).(time.Duration), args.Error(1)
}

func (m *mockOSHelper) GetOsName() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *mockOSHelper) GetUname() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *mockOSHelper) GetArch() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *mockOSHelper) GetMk8sClusterId(string) string {
	args := m.Called()
	return args.String(0)
}

func (m *mockOSHelper) GetSystemdStatus(string) (string, error) {
	return "active", nil
}

type mockVersionServiceClient struct {
	mock.Mock
}

func (m *mockVersionServiceClient) GetVersion(ctx context.Context, req *agentmanager.GetVersionRequest, opts ...grpc.CallOption) (*agentmanager.GetVersionResponse, error) {
	args := m.Called(ctx, req, opts)

	// Handle the case where a nil response is returned
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*agentmanager.GetVersionResponse), args.Error(1)
}

// New mock for agents.AgentData
type mockAgentData struct {
	mock.Mock
}

func (m *mockAgentData) GetAgentType() agentmanager.AgentType {
	args := m.Called()
	return args.Get(0).(agentmanager.AgentType)
}

func (m *mockAgentData) GetDebPackageName() string {
	args := m.Called()
	return args.String(0)
}

func (m *mockAgentData) GetServiceName() string {
	args := m.Called()
	return args.String(0)
}
func (m *mockAgentData) IsAgentHealthy() (bool, healthcheck.Response) {
	args := m.Called()
	return args.Bool(0), args.Get(1).(healthcheck.Response)
}

func (m *mockAgentData) Update(string, string) error {
	args := m.Called()
	return args.Error(0)
}
func (m *mockAgentData) Restart() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockAgentData) GetLastUpdateError() error {
	args := m.Called()
	return args.Error(0)
}
func TestNew(t *testing.T) {
	metadata := &mockMetadataReader{}
	oh := &mockOSHelper{}
	cfg := config.Config{
		GRPC: clientconfig.GRPCConfig{
			Endpoint: "localhost:50051",
			Insecure: true,
			Timeout:  5 * time.Second,
		},
	}

	client, err := New(metadata, oh, &cfg, nil, tokenFunc)
	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.NotNil(t, client.conn)
	assert.NotNil(t, client.client)
}

func TestSendAgentData(t *testing.T) {
	metadata := &mockMetadataReader{}
	oh := &mockOSHelper{}
	mockClient := &mockVersionServiceClient{}

	client := &Client{
		metadata:     metadata,
		oh:           oh,
		client:       mockClient,
		retryBackoff: getRetryBackoff(clientconfig.GetDefaultRetryConfig()),
		config: &config.Config{
			GRPC: clientconfig.GRPCConfig{
				Timeout: 5 * time.Second,
			},
		},
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Set up mock expectations
	metadata.On("GetParentId").Return("parent-123", nil)
	metadata.On("GetInstanceId").Return("instance-456", nil)
	oh.On("GetDebVersion", mock.Anything).Return("1.0.0", nil)
	oh.On("GetServiceUptime", mock.Anything).Return(10*time.Minute, nil)
	oh.On("GetSystemUptime").Return(1*time.Hour, nil)
	oh.On("GetOsName").Return("Linux", nil)
	oh.On("GetUname").Return("Linux 5.4.0-generic", nil)
	oh.On("GetArch").Return("x86_64", nil)
	oh.On("GetMk8sClusterId").Return("abcd", nil)

	expectedResponse := &agentmanager.GetVersionResponse{
		Action: agentmanager.Action_NOP,
	}
	mockClient.On("GetVersion", mock.Anything, mock.Anything, mock.Anything).Return(expectedResponse, nil)

	agentData := &mockAgentData{}
	agentData.On("GetServiceName").Return("test-agent")
	agentData.On("GetDebPackageName").Return("test-agent-package")
	agentData.On("GetAgentType").Return(agentmanager.AgentType_O11Y_AGENT)
	agentData.On("IsAgentHealthy").Return(true, healthcheck.Response{})
	agentData.On("GetLastUpdateError").Return(nil)

	response, err := client.SendAgentData(agentData)

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse, response)

	// Verify mock expectations
	metadata.AssertExpectations(t)
	oh.AssertExpectations(t)
	mockClient.AssertExpectations(t)
	agentData.AssertExpectations(t)
}

// nolint: gocognit
func TestFillRequest(t *testing.T) {
	metadata := &mockMetadataReader{}
	oh := &mockOSHelper{}

	client := &Client{
		metadata:     metadata,
		oh:           oh,
		retryBackoff: getRetryBackoff(clientconfig.GetDefaultRetryConfig()),
		logger:       slog.New(slog.NewTextHandler(os.Stdout, nil)),
		config:       &config.Config{},
	}

	// Set up mock expectations
	metadata.On("GetParentId").Return("parent-123", nil)
	metadata.On("GetInstanceId").Return("instance-456", nil)
	oh.On("GetDebVersion", mock.Anything).Return("1.0.0", nil)
	oh.On("GetServiceUptime", mock.Anything).Return(10*time.Minute, nil)
	oh.On("GetSystemUptime").Return(1*time.Hour, nil)
	oh.On("GetOsName").Return("Linux", nil)
	oh.On("GetUname").Return("Linux 5.4.0-generic", nil)
	oh.On("GetArch").Return("x86_64", nil)
	oh.On("GetMk8sClusterId", mock.Anything).Return("abcd", nil)

	// Create mock health check response using the correct structure
	checkStatuses := map[string]healthcheck.CheckStatus{
		"process": {
			IsOk:    true,
			Reasons: []string{"Process is running"},
			Parameters: []healthcheck.Parameter{
				{Name: "pid", Value: "1234"},
			},
		},
		"cpu": {
			IsOk:    true,
			Reasons: []string{"CPU usage is normal"},
			Parameters: []healthcheck.Parameter{
				{Name: "usage", Value: "45%"},
			},
		},
		"gpu": {
			IsOk:    false,
			Reasons: []string{"GPU driver not found"},
		},
	}

	healthResponse := healthcheck.Response{
		StatusMsg:     "healthy",
		UpSince:       time.Now().Add(-10 * time.Minute),
		Uptime:        "10m0s",
		Reasons:       []string{"Agent is healthy"},
		CheckStatuses: checkStatuses,
	}

	agentData := &mockAgentData{}
	agentData.On("GetServiceName").Return("test-agent")
	agentData.On("GetDebPackageName").Return("test-agent-package")
	agentData.On("GetAgentType").Return(agentmanager.AgentType_O11Y_AGENT)
	agentData.On("IsAgentHealthy").Return(true, healthResponse)
	agentData.On("GetLastUpdateError").Return(fmt.Errorf("some-error"))

	req := client.fillRequest(agentData)

	assert.NotNil(t, req)
	assert.Equal(t, agentmanager.AgentType_O11Y_AGENT, req.Type)
	assert.Equal(t, "1.0.0", req.AgentVersion)
	assert.Equal(t, "1.0.0", req.UpdaterVersion)
	assert.Equal(t, "parent-123", req.ParentId)
	assert.Equal(t, "instance-456", req.InstanceId)
	assert.Equal(t, "Linux", req.OsInfo.Name)
	assert.Equal(t, "Linux 5.4.0-generic", req.OsInfo.Uname)
	assert.Equal(t, "x86_64", req.OsInfo.Architecture)
	assert.Equal(t, agentmanager.AgentState_STATE_HEALTHY, req.AgentState)
	assert.Equal(t, []string{"Agent is healthy"}, req.AgentStateMessages)
	assert.Equal(t, durationpb.New(10*time.Minute), req.AgentUptime)
	assert.Equal(t, durationpb.New(10*time.Minute), req.UpdaterUptime)
	assert.Equal(t, durationpb.New(1*time.Hour), req.SystemUptime)
	assert.Equal(t, "some-error", req.LastUpdateError)
	assert.Equal(t, "abcd", req.Mk8SClusterId)

	// Verify module health statuses
	assert.NotNil(t, req.ModulesHealth)
	assert.Equal(t, agentmanager.AgentState_STATE_HEALTHY, req.ModulesHealth.Process.State)
	assert.Equal(t, []string{"Process is running"}, req.ModulesHealth.Process.Messages)
	assert.Equal(t, agentmanager.AgentState_STATE_HEALTHY, req.ModulesHealth.CpuPipeline.State)
	assert.Equal(t, []string{"CPU usage is normal"}, req.ModulesHealth.CpuPipeline.Messages)
	assert.Equal(t, agentmanager.AgentState_STATE_ERROR, req.ModulesHealth.GpuPipeline.State)
	assert.Equal(t, []string{"GPU driver not found"}, req.ModulesHealth.GpuPipeline.Messages)

	// Verify mock expectations
	metadata.AssertExpectations(t)
	oh.AssertExpectations(t)
	agentData.AssertExpectations(t)
}

func TestSendAgentDataWithRetry(t *testing.T) {
	metadata := &mockMetadataReader{}
	oh := &mockOSHelper{}
	mockClient := &mockVersionServiceClient{}

	client := &Client{
		metadata: metadata,
		oh:       oh,
		client:   mockClient,
		config: &config.Config{
			GRPC: clientconfig.GRPCConfig{
				Timeout: 5 * time.Second,
				Retry: clientconfig.RetryConfig{
					Enabled:        true,
					MaxElapsedTime: 15 * time.Second,
				},
			},
		},
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	client.retryBackoff = getRetryBackoff(client.config.GRPC.Retry)

	// Set up mock expectations
	metadata.On("GetParentId").Return("parent-123", nil)
	metadata.On("GetInstanceId").Return("instance-456", nil)
	oh.On("GetDebVersion", mock.Anything).Return("1.0.0", nil)
	oh.On("GetServiceUptime", mock.Anything).Return(10*time.Minute, nil)
	oh.On("GetSystemUptime").Return(1*time.Hour, nil)
	oh.On("GetOsName").Return("Linux", nil)
	oh.On("GetUname").Return("Linux 5.4.0-generic", nil)
	oh.On("GetArch").Return("x86_64", nil)
	oh.On("GetMk8sClusterId").Return("abcd", nil)

	expectedResponse := &agentmanager.GetVersionResponse{
		Action: agentmanager.Action_NOP,
	}

	// Simulate two failures followed by a success
	mockClient.On("GetVersion", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, status.Error(codes.Unavailable, "Service unavailable")).Once()
	mockClient.On("GetVersion", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, status.Error(codes.Unavailable, "Service unavailable")).Once()
	mockClient.On("GetVersion", mock.Anything, mock.Anything, mock.Anything).
		Return(expectedResponse, nil).Once()

	agentData := &mockAgentData{}
	agentData.On("GetServiceName").Return("test-agent")
	agentData.On("GetDebPackageName").Return("test-agent-package")
	agentData.On("GetAgentType").Return(agentmanager.AgentType_O11Y_AGENT)
	agentData.On("IsAgentHealthy").Return(true, healthcheck.Response{})
	agentData.On("GetLastUpdateError").Return(nil)

	response, err := client.SendAgentData(agentData)

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse, response)

	// Verify that the GetVersion method was called 3 times
	mockClient.AssertNumberOfCalls(t, "GetVersion", 3)

	// Verify other mock expectations
	metadata.AssertExpectations(t)
	oh.AssertExpectations(t)
	mockClient.AssertExpectations(t)
	agentData.AssertExpectations(t)
}

func TestSendAgentDataWithRetryFailure(t *testing.T) {
	metadata := &mockMetadataReader{}
	oh := &mockOSHelper{}
	mockClient := &mockVersionServiceClient{}

	client := &Client{
		metadata: metadata,
		oh:       oh,
		client:   mockClient,
		config: &config.Config{
			GRPC: clientconfig.GRPCConfig{
				Timeout: 5 * time.Second,
				Retry: clientconfig.RetryConfig{
					Enabled:             true,
					MaxElapsedTime:      15 * time.Second,
					InitialInterval:     1 * time.Second,
					Multiplier:          2,
					RandomizationFactor: 0,
				},
			},
		},
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}
	client.retryBackoff = getRetryBackoff(client.config.GRPC.Retry)

	// Set up mock expectations (same as in the previous test)
	metadata.On("GetParentId").Return("parent-123", nil)
	metadata.On("GetInstanceId").Return("instance-456", nil)
	oh.On("GetDebVersion", mock.Anything).Return("1.0.0", nil)
	oh.On("GetServiceUptime", mock.Anything).Return(10*time.Minute, nil)
	oh.On("GetSystemUptime").Return(1*time.Hour, nil)
	oh.On("GetOsName").Return("Linux", nil)
	oh.On("GetUname").Return("Linux 5.4.0-generic", nil)
	oh.On("GetArch").Return("x86_64", nil)
	oh.On("GetMk8sClusterId").Return("abcd", nil)

	// Simulate continuous failures
	mockClient.On("GetVersion", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, status.Error(codes.Unavailable, "Service unavailable")).Times(4)

	agentData := &mockAgentData{}
	agentData.On("GetServiceName").Return("test-agent")
	agentData.On("GetDebPackageName").Return("test-agent-package")
	agentData.On("GetAgentType").Return(agentmanager.AgentType_O11Y_AGENT)
	agentData.On("IsAgentHealthy").Return(true, healthcheck.Response{})
	agentData.On("GetLastUpdateError").Return(nil)

	response, err := client.SendAgentData(agentData)

	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "all retries failed")

	// Verify that the GetVersion method was called multiple times
	mockClient.AssertNumberOfCalls(t, "GetVersion", 4)

	// Verify other mock expectations
	metadata.AssertExpectations(t)
	oh.AssertExpectations(t)
	mockClient.AssertExpectations(t)
	agentData.AssertExpectations(t)
}

func TestFillRequestDebNotFound(t *testing.T) {
	metadata := &mockMetadataReader{}
	oh := &mockOSHelper{}

	client := &Client{
		metadata:     metadata,
		oh:           oh,
		logger:       slog.New(slog.NewTextHandler(os.Stdout, nil)),
		retryBackoff: getRetryBackoff(clientconfig.GetDefaultRetryConfig()),
		config:       &config.Config{},
	}

	agentData := &mockAgentData{}
	agentData.On("GetServiceName").Return("test-agent")
	agentData.On("GetDebPackageName").Return("test-agent-package")
	agentData.On("GetAgentType").Return(agentmanager.AgentType_O11Y_AGENT)
	agentData.On("IsAgentHealthy").Return(true, healthcheck.Response{})
	agentData.On("GetLastUpdateError").Return(nil)

	// Set up mock expectations
	oh.On("GetDebVersion", "test-agent-package").Return("", osutils.ErrDebNotFound)
	oh.On("GetDebVersion", "nebius-observability-agent-updater").Return("", osutils.ErrDebNotFound)
	metadata.On("GetParentId").Return("parent-123", nil)
	metadata.On("GetInstanceId").Return("instance-456", nil)
	oh.On("GetOsName").Return("Linux", nil)
	oh.On("GetUname").Return("Linux 5.4.0-generic", nil)
	oh.On("GetArch").Return("x86_64", nil)
	oh.On("GetServiceUptime", mock.Anything).Return(10*time.Minute, nil)
	oh.On("GetSystemUptime").Return(1*time.Hour, nil)
	oh.On("GetMk8sClusterId").Return("abcd", nil)

	req := client.fillRequest(agentData)

	assert.NotNil(t, req)
	assert.Equal(t, "", req.AgentVersion)
	assert.Equal(t, "", req.UpdaterVersion)

	// Verify expectations
	metadata.AssertExpectations(t)
	oh.AssertExpectations(t)
	agentData.AssertExpectations(t)
}
