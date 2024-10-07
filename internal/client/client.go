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
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/durationpb"
	"log/slog"
	"os"
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

const (
	ENDPOINT_ENV = "NEBIUS_OBSERVABILITY_AGENT_UPDATER_ENDPOINT"
)

type GRPCConfig struct {
	Endpoint string        `yaml:"endpoint"`
	TLS      TLSConfig     `yaml:"tls"`
	Insecure bool          `yaml:"insecure"`
	Timeout  time.Duration `yaml:"timeout"`
	Retry    RetryConfig   `yaml:"retry"`
}

func GetDefaultGrpcConfig() GRPCConfig {
	return GRPCConfig{
		Endpoint: "observability-agent-manager.eu-north1.nebius.cloud:443",
		Insecure: false,
		Timeout:  5 * time.Second,
		Retry:    GetDefaultRetryConfig(),
	}
}

type RetryConfig struct {
	MaxElapsedTime      time.Duration `yaml:"max_elapsed_time"`
	InitialInterval     time.Duration `yaml:"initial_interval"`
	Multiplier          float64       `yaml:"multiplier"`
	RandomizationFactor float64       `yaml:"randomization_factor"`
}

func GetDefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxElapsedTime:      time.Second * 30,
		InitialInterval:     time.Second,
		Multiplier:          1.5,
		RandomizationFactor: 0.5,
	}
}

type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	CAFile   string `yaml:"ca_file"`
}

type Client struct {
	metadata         metadataReader
	config           *GRPCConfig
	conn             *grpc.ClientConn
	client           generated.VersionServiceClient
	logger           *slog.Logger
	oh               oshelper
	retryBackoff     backoff.BackOff
	getTokenCallback func() (string, error)
}

func New(metadata metadataReader, oh oshelper, config *GRPCConfig, logger *slog.Logger, getTokenCallback func() (string, error)) (*Client, error) {
	if config.Endpoint == "" {
		endpoint := os.Getenv(ENDPOINT_ENV)
		if endpoint == "" {
			return nil, fmt.Errorf("endpoint is not set")
		}
		config.Endpoint = endpoint
	}
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
		return nil, fmt.Errorf("failed to create grpc client to %s: %w", config.Endpoint, err)
	}
	client := generated.NewVersionServiceClient(conn)

	return &Client{
		metadata:         metadata,
		config:           config,
		conn:             conn,
		client:           client,
		logger:           logger,
		oh:               oh,
		retryBackoff:     getRetryBackoff(config.Retry),
		getTokenCallback: getTokenCallback,
	}, nil
}

func getRetryBackoff(config RetryConfig) backoff.BackOff {
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

func (s *Client) SendAgentData(agent agents.AgentData) (*generated.GetVersionResponse, error) {
	s.logger.Debug("Sending agent data", "agent", agent.GetServiceName())
	req := s.fillRequest(agent)
	var response *generated.GetVersionResponse
	operation := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), s.config.Timeout)
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
			s.logger.Warn("gRPC call failed, retrying", "error", err)
			return err
		}
		response = r
		return nil
	}
	err := backoff.Retry(operation, s.retryBackoff)
	s.retryBackoff.Reset()
	if err != nil {
		return nil, fmt.Errorf("all retries failed: %w", err)
	}

	s.logger.Debug("Received response", "action", response.Action)
	return response, nil
}

func (s *Client) fillRequest(agent agents.AgentData) *generated.GetVersionRequest {
	req := generated.GetVersionRequest{}
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

	instanceId, err := s.metadata.GetInstanceId()
	if err != nil {
		s.logger.Error("failed to get instance id", "error", err)
	} else {
		req.InstanceId = instanceId
	}
	osinfo := generated.OSInfo{}
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

	if agent.IsAgentHealthy() {
		req.AgentState = generated.AgentState_STATE_HEALTHY
	} else {
		req.AgentState = generated.AgentState_STATE_ERROR
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
	return &req
}
