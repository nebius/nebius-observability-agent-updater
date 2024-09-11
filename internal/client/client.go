package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/cenkalti/backoff/v4"
	generated "github.com/nebius/nebius-observability-agent-updater/generated/proto"
	"github.com/nebius/nebius-observability-agent-updater/internal/agents"
	"github.com/nebius/nebius-observability-agent-updater/internal/constants"
	"github.com/nebius/nebius-observability-agent-updater/internal/osutils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/durationpb"
	"log/slog"
	"time"
)

type metadataReader interface {
	GetParentId() (string, error)
	GetInstanceId() (string, error)
	GetIamToken() (string, error)
}

type oshelper interface {
	GetDebVersion(packageName string) (string, error)
	GetServiceUptime(serviceName string) (time.Duration, error)
	GetSystemUptime() (time.Duration, error)
	GetOsName() (string, error)
	GetUname() (string, error)
	GetArch() (string, error)
}

type GRPCConfig struct {
	Endpoint string        `yaml:"endpoint"`
	TLS      TLSConfig     `yaml:"tls"`
	Insecure bool          `yaml:"insecure"`
	Timeout  time.Duration `yaml:"timeout"`
	Retry    RetryConfig   `yaml:"retry"`
}

type RetryConfig struct {
	MaxElapsedTime      time.Duration `yaml:"max_elapsed_time"`
	InitialInterval     time.Duration `yaml:"initial_interval"`
	Multiplier          float64       `yaml:"multiplier"`
	RandomizationFactor float64       `yaml:"randomization_factor"`
}

type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	CAFile   string `yaml:"ca_file"`
}

type Client struct {
	metadata metadataReader
	config   *GRPCConfig
	conn     *grpc.ClientConn
	client   generated.VersionServiceClient
	logger   *slog.Logger
	oh       oshelper
}

func New(metadata metadataReader, oh oshelper, config *GRPCConfig, logger *slog.Logger) (*Client, error) {
	var dialOptions []grpc.DialOption
	if config.Insecure {
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		creds := credentials.NewTLS(&tls.Config{})
		// FIXME fill from config
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(creds))
	}
	conn, err := grpc.NewClient(config.Endpoint, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc clienti to %s: %w", config.Endpoint, err)
	}
	client := generated.NewVersionServiceClient(conn)
	return &Client{
		metadata: metadata,
		config:   config,
		conn:     conn,
		client:   client,
		logger:   logger,
		oh:       oh,
	}, nil
}

func (s *Client) Close() {
	if s.conn != nil {
		_ = s.conn.Close()
	}
}

func (s *Client) SendAgentData(agent agents.AgentData, isAgentHealthy bool) (*generated.GetVersionResponse, error) {
	s.logger.Debug("Sending agent data", "agent", agent.GetServiceName())
	req, err := s.fillRequest(agent, isAgentHealthy)
	if err != nil {
		return nil, err
	}
	var response *generated.GetVersionResponse
	operation := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), s.config.Timeout)
		defer cancel()
		r, err := s.client.GetVersion(ctx, req)
		if err != nil {
			s.logger.Warn("gRPC call failed, retrying", "error", err)
			return err
		}
		response = r
		return nil
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = s.config.Retry.MaxElapsedTime
	b.RandomizationFactor = s.config.Retry.RandomizationFactor
	b.InitialInterval = s.config.Retry.InitialInterval
	b.Multiplier = s.config.Retry.Multiplier

	err = backoff.Retry(operation, b)
	if err != nil {
		return nil, fmt.Errorf("all retries failed: %w", err)
	}

	s.logger.Debug("Received response", "action", response.Action)
	return response, nil
}

func (s *Client) fillRequest(agent agents.AgentData, isAgentHealthy bool) (*generated.GetVersionRequest, error) {
	req := generated.GetVersionRequest{}
	req.Type = agent.GetAgentType()

	agentVersion, err := s.oh.GetDebVersion(agent.GetDebPackageName())
	if err != nil {
		if !errors.Is(err, osutils.ErrDebNotFound) {
			return nil, fmt.Errorf("failed to get agent version: %w", err)
		}
		agentVersion = ""
	}
	req.AgentVersion = agentVersion

	updaterVersion, err := s.oh.GetDebVersion(constants.UpdaterDebPackageName)
	if err != nil {
		if !errors.Is(err, osutils.ErrDebNotFound) {
			return nil, fmt.Errorf("failed to get updater version: %w", err)
		}
		updaterVersion = ""
	}

	req.UpdaterVersion = updaterVersion

	parentId, err := s.metadata.GetParentId()
	if err != nil {
		return nil, fmt.Errorf("failed to get parent id: %w", err)
	}
	req.ParentId = parentId

	instanceId, err := s.metadata.GetInstanceId()
	if err != nil {
		return nil, fmt.Errorf("failed to get instance id: %w", err)
	}
	req.InstanceId = instanceId
	osinfo := generated.OSInfo{}
	osName, err := s.oh.GetOsName()
	if err != nil {
		return nil, fmt.Errorf("failed to get os name: %w", err)
	}
	osinfo.Name = osName

	uname, err := s.oh.GetUname()
	if err != nil {
		return nil, fmt.Errorf("failed to get uname: %w", err)
	}

	osinfo.Uname = uname

	arch, err := s.oh.GetArch()
	if err != nil {
		return nil, fmt.Errorf("failed to get arch: %w", err)
	}
	osinfo.Architecture = arch

	req.OsInfo = &osinfo

	if isAgentHealthy {
		req.AgentState = generated.AgentState_STATE_HEALTHY
	} else {
		req.AgentState = generated.AgentState_STATE_ERROR
	}

	agentUptime, err := s.oh.GetServiceUptime(agent.GetServiceName())
	if err != nil {
		return nil, fmt.Errorf("failed to get agent uptime: %w", err)
	}
	req.AgentUptime = durationpb.New(agentUptime)

	updaterUptime, err := s.oh.GetServiceUptime(constants.UpdaterServiceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get updater uptime: %w", err)
	}
	req.UpdaterUptime = durationpb.New(updaterUptime)

	systemUptime, err := s.oh.GetSystemUptime()
	if err != nil {
		return nil, fmt.Errorf("failed to get system uptime: %w", err)
	}
	req.SystemUptime = durationpb.New(systemUptime)
	return &req, nil
}
