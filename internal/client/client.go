package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/cenkalti/backoff/v4"
	"github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"
	"github.com/nebius/nebius-observability-agent-updater/internal/agents"
	"github.com/nebius/nebius-observability-agent-updater/internal/client/clientconfig"
	"github.com/nebius/nebius-observability-agent-updater/internal/config"
	"github.com/nebius/nebius-observability-agent-updater/internal/constants"
	"github.com/nebius/nebius-observability-agent-updater/internal/healthcheck"
	"github.com/nebius/nebius-observability-agent-updater/internal/osutils"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/durationpb"
	"log/slog"
	"os"
	"time"
)

type metadataReader interface {
	GetParentId() (string, error)
	GetInstanceId() (string, bool, error)
	GetIamToken() (string, error)
}

type oshelper interface {
	GetDebVersion(packageName string) (string, error)
	GetServiceUptime(serviceName string) (time.Duration, error)
	GetSystemUptime() (time.Duration, error)
	GetSystemdStatus(serviceName string) (string, error)
	GetOsName() (string, error)
	GetUname() (string, error)
	GetArch() (string, error)
	GetMk8sClusterId(path string) string
	GetLastLogs(serviceName string, lines int) (string, error)
}

type dcgmhelper interface {
	GetDCGMVersion() (string, error)
	GetGpuInfo() (model string, number int, err error)
}

const (
	ENDPOINT_ENV     = "NEBIUS_OBSERVABILITY_AGENT_UPDATER_ENDPOINT"
	UserAgent        = "nebius-observability-agent-updater"
	ProcessHealthKey = "process"
	CpuHealthKey     = "cpu"
	GpuHealthKey     = "gpu"
	CiliumHealthKey  = "cilium"
)

type Client struct {
	metadata         metadataReader
	config           *config.Config
	conn             *grpc.ClientConn
	client           agentmanager.VersionServiceClient
	logger           *slog.Logger
	oh               oshelper
	dh               dcgmhelper
	retryBackoff     backoff.BackOff
	getTokenCallback func() (string, error)
}

func New(metadata metadataReader, oh oshelper, dh dcgmhelper, config *config.Config, logger *slog.Logger, getTokenCallback func() (string, error)) (*Client, error) {
	if config.GRPC.Endpoint == "" {
		endpoint := os.Getenv(ENDPOINT_ENV)
		if endpoint == "" {
			return nil, fmt.Errorf("endpoint is not set")
		}
		config.GRPC.Endpoint = endpoint
	}
	var dialOptions []grpc.DialOption
	creds := credentials.NewTLS(&tls.Config{})
	// FIXME fill from config
	dialOptions = append(dialOptions, grpc.WithTransportCredentials(creds))

	dialOptions = append(dialOptions, grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:                config.GRPC.KeepAlive.Time,
		Timeout:             config.GRPC.KeepAlive.Timeout,
		PermitWithoutStream: config.GRPC.KeepAlive.PermitWithoutStream,
	}))

	dialOptions = append(dialOptions, grpc.WithUserAgent(UserAgent))

	conn, err := grpc.NewClient("dns:///"+config.GRPC.Endpoint, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc client to %s: %w", config.GRPC.Endpoint, err)
	}
	client := agentmanager.NewVersionServiceClient(conn)

	return &Client{
		metadata:         metadata,
		config:           config,
		conn:             conn,
		client:           client,
		logger:           logger,
		oh:               oh,
		dh:               dh,
		retryBackoff:     getRetryBackoff(config.GRPC.Retry),
		getTokenCallback: getTokenCallback,
	}, nil
}

func getRetryBackoff(config clientconfig.RetryConfig) backoff.BackOff {
	retryBackoff := backoff.NewExponentialBackOff()
	retryBackoff.MaxElapsedTime = config.MaxElapsedTime
	retryBackoff.RandomizationFactor = config.RandomizationFactor
	retryBackoff.InitialInterval = config.InitialInterval
	retryBackoff.Multiplier = config.Multiplier
	return retryBackoff
}

func (s *Client) Close() {
	if s.conn != nil {
		_ = s.conn.Close()
	}
}

func (s *Client) SendAgentData(agent agents.AgentData) (*agentmanager.GetVersionResponse, error) {
	s.logger.Debug("Sending agent data", "agent", agent.GetServiceName())
	req := s.fillRequest(agent)
	var response *agentmanager.GetVersionResponse
	operation := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), s.config.GRPC.Timeout)
		defer cancel()
		if s.getTokenCallback != nil {
			authToken, err := s.getTokenCallback()
			if err != nil {
				s.logger.Warn("failed to get auth token, sending request with empty token", "error", err)
				authToken = ""
			}
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+authToken)
		}
		r, err := s.client.GetVersion(ctx, req)
		if err != nil {
			s.logger.Warn("gRPC call failed", "error", err)
			return err
		}
		response = r
		return nil
	}
	if s.config.GRPC.Retry.Enabled {
		err := backoff.Retry(operation, s.retryBackoff)
		s.retryBackoff.Reset()
		if err != nil {
			return nil, fmt.Errorf("all retries failed: %w", err)
		}
	} else {
		err := operation()
		if err != nil {
			return nil, fmt.Errorf("failed to send agent data: %w", err)
		}
	}

	s.logger.Debug("Received response", "action", response.Action)
	return response, nil
}

func (s *Client) processModuleHealth(healthKey string, statuses map[string]healthcheck.CheckStatus) (isError bool, moduleHealth *agentmanager.ModuleHealth) {
	if health, found := statuses[healthKey]; found {
		state := agentmanager.AgentState_STATE_HEALTHY
		if !health.IsOk {
			isError = true
			state = agentmanager.AgentState_STATE_ERROR
		}
		var params []*agentmanager.Parameter
		for _, p := range health.Parameters {
			params = append(params, &agentmanager.Parameter{
				Name:  p.Name,
				Value: p.Value,
			})
		}
		return isError, &agentmanager.ModuleHealth{
			State:      state,
			Messages:   health.Reasons,
			Parameters: params,
		}
	}
	return false, nil
}

// nolint: gocognit
func (s *Client) fillRequest(agent agents.AgentData) *agentmanager.GetVersionRequest {
	req := agentmanager.GetVersionRequest{}
	req.Type = agent.GetAgentType()

	agentVersion, err := s.oh.GetDebVersion(agent.GetDebPackageName())
	if err != nil {
		if !errors.Is(err, osutils.ErrDebNotFound) {
			s.logger.Error("failed to get agent version", "error", err)
		} else {
			s.logger.Info("agent is not installed", "package", agent.GetDebPackageName())
		}
	} else {
		req.AgentVersion = agentVersion
	}

	updaterVersion, err := s.oh.GetDebVersion(constants.UpdaterDebPackageName)
	if err != nil {
		if !errors.Is(err, osutils.ErrDebNotFound) {
			s.logger.Error("failed to get updater version", "error", err)
		} else {
			s.logger.Info("updater is not installed", "package", constants.UpdaterDebPackageName)
		}
	} else {
		req.UpdaterVersion = updaterVersion
	}

	parentId, err := s.metadata.GetParentId()
	if err != nil {
		s.logger.Error("failed to get parent id", "error", err)
	} else {
		req.ParentId = parentId
	}

	instanceId, cloudInitFallback, err := s.metadata.GetInstanceId()
	if err != nil {
		s.logger.Error("failed to get instance id", "error", err)
	} else {
		req.InstanceId = instanceId
	}

	req.InstanceIdUsedFallback = cloudInitFallback
	osinfo := agentmanager.OSInfo{}
	osName, err := s.oh.GetOsName()
	if err != nil {
		s.logger.Error("failed to get os name", "error", err)
	} else {
		osinfo.Name = osName
	}

	uname, err := s.oh.GetUname()
	if err != nil {
		s.logger.Error("failed to get uname", "error", err)
	} else {
		osinfo.Uname = uname
	}

	arch, err := s.oh.GetArch()
	if err != nil {
		s.logger.Error("failed to get arch", "error", err)
	} else {
		osinfo.Architecture = arch
	}

	req.OsInfo = &osinfo
	healthy, response := agent.IsAgentHealthy()
	if healthy {
		req.AgentState = agentmanager.AgentState_STATE_HEALTHY
	} else {
		req.AgentState = agentmanager.AgentState_STATE_ERROR
	}
	req.AgentStateMessages = response.Reasons
	req.ModulesHealth = &agentmanager.ModulesHealth{}
	if processHealth, found := response.CheckStatuses[ProcessHealthKey]; found {
		state := agentmanager.AgentState_STATE_HEALTHY
		if !processHealth.IsOk {
			state = agentmanager.AgentState_STATE_ERROR
		}
		var params []*agentmanager.Parameter
		for _, p := range processHealth.Parameters {
			params = append(params, &agentmanager.Parameter{
				Name:  p.Name,
				Value: p.Value,
			})
		}
		req.ModulesHealth.Process = &agentmanager.ModuleHealth{
			State:      state,
			Messages:   processHealth.Reasons,
			Parameters: params,
		}
	}
	cpuError := false
	cpuError, req.ModulesHealth.CpuPipeline = s.processModuleHealth(CpuHealthKey, response.CheckStatuses)
	gpuError := false
	gpuError, req.ModulesHealth.GpuPipeline = s.processModuleHealth(GpuHealthKey, response.CheckStatuses)
	ciliumError := false
	ciliumError, req.ModulesHealth.CiliumPipeline = s.processModuleHealth(CiliumHealthKey, response.CheckStatuses)

	if req.AgentState != agentmanager.AgentState_STATE_HEALTHY && !gpuError && !cpuError && !ciliumError {
		lastLogs, err := s.oh.GetLastLogs(agent.GetServiceName(), 10)
		if err != nil {
			s.logger.Error("failed to get last logs", "error", err)
		} else {
			req.LastAgentLogs = lastLogs
		}
	}

	agentUptime, err := s.oh.GetServiceUptime(agent.GetServiceName())
	if err != nil {
		s.logger.Error("failed to get agent uptime", "error", err)
	} else {
		req.AgentUptime = durationpb.New(agentUptime)
	}

	updaterUptime, err := s.oh.GetServiceUptime(constants.UpdaterServiceName)
	if err != nil {
		s.logger.Error("failed to get updater uptime", "error", err)
	} else {
		req.UpdaterUptime = durationpb.New(updaterUptime)
	}

	systemUptime, err := s.oh.GetSystemUptime()
	if err != nil {
		s.logger.Error("failed to get system uptime", "error", err)
	} else {
		req.SystemUptime = durationpb.New(systemUptime)
	}

	lastError := agent.GetLastUpdateError()
	if lastError != nil {
		req.LastUpdateError = lastError.Error()
	}

	req.Mk8SClusterId = s.oh.GetMk8sClusterId(s.config.Mk8sClusterIdPath)

	cloudInitStatus, err := s.oh.GetSystemdStatus(constants.CloudInitServiceName)
	if err != nil {
		s.logger.Error("failed to get cloud-init status", "error", err)
	} else {
		req.CloudInitStatus = cloudInitStatus
	}

	dcgmVersion, err := s.dh.GetDCGMVersion()
	if err != nil {
		s.logger.Error("failed to get DCGM version", "error", err)
	} else {
		req.DcgmVersion = dcgmVersion
	}

	gpuModel, gpuNumber, err := s.dh.GetGpuInfo()
	if err != nil {
		s.logger.Error("failed to get GPU info", "error", err)
	} else {
		req.GpuModel = gpuModel
		req.GpuNumber = int32(gpuNumber)
	}

	return &req
}
