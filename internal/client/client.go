package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/durationpb"
)

type metadataReader interface {
	GetParentId() (string, error)
	GetInstanceId() (string, bool, error)
	GetIamToken() (string, error)
}

type packageManager interface {
	GetDebVersion(packageName string) (string, error)
}

type systemInfo interface {
	GetServiceUptime(serviceName string) (time.Duration, error)
	GetSystemUptime() (time.Duration, error)
	GetSystemdStatus(serviceName string) (string, error)
	GetOsName() (string, error)
	GetUname() (string, error)
	GetArch() (string, error)
}

type systemLogger interface {
	GetLastLogs(serviceName string, lines int) (string, error)
}

type storageInfo interface {
	GetDirectorySize(path string) (int64, error)
	GetMountpointSize(path string) (int64, error)
}

type clusterInfo interface {
	GetMk8sClusterId(path string) string
}

type oshelper interface {
	packageManager
	systemInfo
	systemLogger
	storageInfo
	clusterInfo
}

type dcgmhelper interface {
	GetDCGMVersion() (string, error)
	GetGpuInfo() (model string, number int, err error)
}

const (
	ENDPOINT_ENV               = "NEBIUS_OBSERVABILITY_AGENT_UPDATER_ENDPOINT"
	UserAgent                  = "nebius-observability-agent-updater"
	ProcessHealthKey           = "process"
	CpuHealthKey               = "cpu"
	GpuHealthKey               = "gpu"
	CiliumHealthKey            = "cilium"
	VmappsHealthKey            = "vmapps"
	CommonServiceLogsHealthKey = "common_service_logs"
	VmServiceLogsHealthKey     = "vm_service_logs"
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
	dialOptions := make([]grpc.DialOption, 0, 3)
	if config.GRPC.Insecure {
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		creds := credentials.NewTLS(&tls.Config{})
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(creds))
	}

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
		params := make([]*agentmanager.Parameter, 0, len(health.Parameters))
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

func (s *Client) fillVersionInfo(req *agentmanager.GetVersionRequest, agent agents.AgentData) {
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
}

func (s *Client) fillMetadataInfo(req *agentmanager.GetVersionRequest) {
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
}

func (s *Client) fillOSInfo(req *agentmanager.GetVersionRequest) {
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
}

func (s *Client) fillHealthInfo(req *agentmanager.GetVersionRequest, agent agents.AgentData) {
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
		params := make([]*agentmanager.Parameter, 0, len(processHealth.Parameters))
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

	var cpuError, gpuError, ciliumError, vmappsError, commonServiceLogsError, vmServiceLogsError bool
	cpuError, req.ModulesHealth.CpuPipeline = s.processModuleHealth(CpuHealthKey, response.CheckStatuses)
	gpuError, req.ModulesHealth.GpuPipeline = s.processModuleHealth(GpuHealthKey, response.CheckStatuses)
	ciliumError, req.ModulesHealth.CiliumPipeline = s.processModuleHealth(CiliumHealthKey, response.CheckStatuses)
	vmappsError, req.ModulesHealth.VmappsPipeline = s.processModuleHealth(VmappsHealthKey, response.CheckStatuses)
	commonServiceLogsError, req.ModulesHealth.CommonServiceLogsPipeline = s.processModuleHealth(CommonServiceLogsHealthKey, response.CheckStatuses)
	vmServiceLogsError, req.ModulesHealth.VmServiceLogsPipeline = s.processModuleHealth(VmServiceLogsHealthKey, response.CheckStatuses)

	if req.AgentState != agentmanager.AgentState_STATE_HEALTHY && !gpuError && !cpuError && !ciliumError && !vmappsError && !commonServiceLogsError && !vmServiceLogsError {
		lastLogs, err := s.oh.GetLastLogs(agent.GetServiceName(), 10)
		if err != nil {
			s.logger.Error("failed to get last logs", "error", err)
		} else {
			req.LastAgentLogs = lastLogs
		}
	}
}

func (s *Client) fillUptimeInfo(req *agentmanager.GetVersionRequest, agent agents.AgentData) {
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
}

func (s *Client) fillGPUInfo(req *agentmanager.GetVersionRequest) {
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
}

func (s *Client) fillHealthCheckLogsInfo(req *agentmanager.GetVersionRequest) {
	if s.config.HealthCheckPath != "" {
		req.HealthcheckLogs = &agentmanager.HealthCheckLogs{}
		dirSize, err := s.oh.GetDirectorySize(s.config.HealthCheckPath)
		if err != nil {
			s.logger.Error("failed to get healthcheck directory size", "error", err)
		} else {
			req.HealthcheckLogs.DirectorySizeBytes = dirSize
		}

		mountpointSize, err := s.oh.GetMountpointSize(s.config.HealthCheckPath)
		if err != nil {
			s.logger.Error("failed to get healthcheck mountpoint size", "error", err)
		} else {
			req.HealthcheckLogs.MountpointTotalBytes = mountpointSize
		}
	}
}

func (s *Client) fillRequest(agent agents.AgentData) *agentmanager.GetVersionRequest {
	req := agentmanager.GetVersionRequest{}
	req.Type = agent.GetAgentType()

	s.fillVersionInfo(&req, agent)
	s.fillMetadataInfo(&req)
	s.fillOSInfo(&req)
	s.fillHealthInfo(&req, agent)
	s.fillUptimeInfo(&req, agent)

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

	s.fillGPUInfo(&req)
	s.fillHealthCheckLogsInfo(&req)

	return &req
}
