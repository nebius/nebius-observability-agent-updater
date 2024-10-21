package client

import (
	"context"
	generated "github.com/nebius/nebius-observability-agent-updater/generated/proto"
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

func (m *mockMetadataReader) GetInstanceId() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
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

type mockVersionServiceClient struct {
	mock.Mock
}

func (m *mockVersionServiceClient) GetVersion(ctx context.Context, req *generated.GetVersionRequest, opts ...grpc.CallOption) (*generated.GetVersionResponse, error) {
	args := m.Called(ctx, req, opts)

	// Handle the case where a nil response is returned
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*generated.GetVersionResponse), args.Error(1)
}

// New mock for agents.AgentData
type mockAgentData struct {
	mock.Mock
}

func (m *mockAgentData) GetAgentType() generated.AgentType {
	args := m.Called()
	return args.Get(0).(generated.AgentType)
}

func (m *mockAgentData) GetDebPackageName() string {
	args := m.Called()
	return args.String(0)
}

func (m *mockAgentData) GetServiceName() string {
	args := m.Called()
	return args.String(0)
}
func (m *mockAgentData) IsAgentHealthy() (bool, []string) {
	args := m.Called()
	return args.Bool(0), args.Get(1).([]string)
}

func (m *mockAgentData) Update(string) error {
	args := m.Called()
	return args.Error(0)
}
func (m *mockAgentData) Restart() error {
	args := m.Called()
	return args.Error(0)
}

func TestNew(t *testing.T) {
	metadata := &mockMetadataReader{}
	oh := &mockOSHelper{}
	config := &GRPCConfig{
		Endpoint: "localhost:50051",
		Insecure: true,
		Timeout:  5 * time.Second,
	}

	client, err := New(metadata, oh, config, nil, tokenFunc)
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
		retryBackoff: getRetryBackoff(GetDefaultRetryConfig()),
		config: &GRPCConfig{
			Timeout: 5 * time.Second,
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

	expectedResponse := &generated.GetVersionResponse{
		Action: generated.Action_NOP,
	}
	mockClient.On("GetVersion", mock.Anything, mock.Anything, mock.Anything).Return(expectedResponse, nil)

	agentData := &mockAgentData{}
	agentData.On("GetServiceName").Return("test-agent")
	agentData.On("GetDebPackageName").Return("test-agent-package")
	agentData.On("GetAgentType").Return(generated.AgentType_O11Y_AGENT)
	agentData.On("IsAgentHealthy").Return(true, []string{})

	response, err := client.SendAgentData(agentData)

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse, response)

	// Verify mock expectations
	metadata.AssertExpectations(t)
	oh.AssertExpectations(t)
	mockClient.AssertExpectations(t)
	agentData.AssertExpectations(t)
}

func TestFillRequest(t *testing.T) {
	metadata := &mockMetadataReader{}
	oh := &mockOSHelper{}

	client := &Client{
		metadata:     metadata,
		oh:           oh,
		retryBackoff: getRetryBackoff(GetDefaultRetryConfig()),
		logger:       slog.New(slog.NewTextHandler(os.Stdout, nil)),
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

	agentData := &mockAgentData{}
	agentData.On("GetServiceName").Return("test-agent")
	agentData.On("GetDebPackageName").Return("test-agent-package")
	agentData.On("GetAgentType").Return(generated.AgentType_O11Y_AGENT)
	agentData.On("IsAgentHealthy").Return(true, []string{})

	req := client.fillRequest(agentData)

	assert.NotNil(t, req)
	assert.Equal(t, generated.AgentType_O11Y_AGENT, req.Type)
	assert.Equal(t, "1.0.0", req.AgentVersion)
	assert.Equal(t, "1.0.0", req.UpdaterVersion)
	assert.Equal(t, "parent-123", req.ParentId)
	assert.Equal(t, "instance-456", req.InstanceId)
	assert.Equal(t, "Linux", req.OsInfo.Name)
	assert.Equal(t, "Linux 5.4.0-generic", req.OsInfo.Uname)
	assert.Equal(t, "x86_64", req.OsInfo.Architecture)
	assert.Equal(t, generated.AgentState_STATE_HEALTHY, req.AgentState)
	assert.Equal(t, durationpb.New(10*time.Minute), req.AgentUptime)
	assert.Equal(t, durationpb.New(10*time.Minute), req.UpdaterUptime)
	assert.Equal(t, durationpb.New(1*time.Hour), req.SystemUptime)

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
		config: &GRPCConfig{
			Timeout: 5 * time.Second,
			Retry: RetryConfig{
				MaxElapsedTime: 15 * time.Second,
			},
		},
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	client.retryBackoff = getRetryBackoff(client.config.Retry)

	// Set up mock expectations
	metadata.On("GetParentId").Return("parent-123", nil)
	metadata.On("GetInstanceId").Return("instance-456", nil)
	oh.On("GetDebVersion", mock.Anything).Return("1.0.0", nil)
	oh.On("GetServiceUptime", mock.Anything).Return(10*time.Minute, nil)
	oh.On("GetSystemUptime").Return(1*time.Hour, nil)
	oh.On("GetOsName").Return("Linux", nil)
	oh.On("GetUname").Return("Linux 5.4.0-generic", nil)
	oh.On("GetArch").Return("x86_64", nil)

	expectedResponse := &generated.GetVersionResponse{
		Action: generated.Action_NOP,
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
	agentData.On("GetAgentType").Return(generated.AgentType_O11Y_AGENT)
	agentData.On("IsAgentHealthy").Return(true, []string{})

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
		config: &GRPCConfig{
			Timeout: 5 * time.Second,
			Retry: RetryConfig{
				MaxElapsedTime:      15 * time.Second,
				InitialInterval:     1 * time.Second,
				Multiplier:          2,
				RandomizationFactor: 0,
			},
		},
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}
	client.retryBackoff = getRetryBackoff(client.config.Retry)

	// Set up mock expectations (same as in the previous test)
	metadata.On("GetParentId").Return("parent-123", nil)
	metadata.On("GetInstanceId").Return("instance-456", nil)
	oh.On("GetDebVersion", mock.Anything).Return("1.0.0", nil)
	oh.On("GetServiceUptime", mock.Anything).Return(10*time.Minute, nil)
	oh.On("GetSystemUptime").Return(1*time.Hour, nil)
	oh.On("GetOsName").Return("Linux", nil)
	oh.On("GetUname").Return("Linux 5.4.0-generic", nil)
	oh.On("GetArch").Return("x86_64", nil)

	// Simulate continuous failures
	mockClient.On("GetVersion", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, status.Error(codes.Unavailable, "Service unavailable")).Times(4)

	agentData := &mockAgentData{}
	agentData.On("GetServiceName").Return("test-agent")
	agentData.On("GetDebPackageName").Return("test-agent-package")
	agentData.On("GetAgentType").Return(generated.AgentType_O11Y_AGENT)
	agentData.On("IsAgentHealthy").Return(true, []string{})

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
		retryBackoff: getRetryBackoff(GetDefaultRetryConfig()),
	}

	agentData := &mockAgentData{}
	agentData.On("GetServiceName").Return("test-agent")
	agentData.On("GetDebPackageName").Return("test-agent-package")
	agentData.On("GetAgentType").Return(generated.AgentType_O11Y_AGENT)
	agentData.On("IsAgentHealthy").Return(true, []string{})

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

	req := client.fillRequest(agentData)

	assert.NotNil(t, req)
	assert.Equal(t, "", req.AgentVersion)
	assert.Equal(t, "", req.UpdaterVersion)

	// Verify expectations
	metadata.AssertExpectations(t)
	oh.AssertExpectations(t)
	agentData.AssertExpectations(t)
}
