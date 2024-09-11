package client

import (
	"errors"
	generated "github.com/nebius/nebius-observability-agent-updater/generated/proto"
	"github.com/nebius/nebius-observability-agent-updater/internal/constants"
	"github.com/nebius/nebius-observability-agent-updater/internal/debwrapper"
	"os/exec"
	"strings"
)

type agentData interface {
	GetAgentType() generated.AgentType
	GetDebPackageName() string
}

type metadataReader interface {
	GetParentId() (string, error)
	GetInstanceId() (string, error)
	GetIamToken() (string, error)
}

type Client struct {
	metadata metadataReader
}

func New(metadata metadataReader) *Client {
	return &Client{metadata: metadata}
}

func (s *Client) fillRequest(agent agentData, isAgentHealthy bool) (*generated.GetVersionRequest, error) {
	dw := debwrapper.NewDebWrapper()
	req := generated.GetVersionRequest{}
	req.Type = agent.GetAgentType()

	agentVersion, err := dw.GetDebVersion(agent.GetDebPackageName())
	if err != nil {
		if !errors.Is(err, debwrapper.ErrDebNotFound) {
			return nil, err
		}
		agentVersion = ""
	}
	req.AgentVersion = agentVersion

	updaterVersion, err := dw.GetDebVersion(constants.UpdaterDebPackageName)
	if err != nil {
		if !errors.Is(err, debwrapper.ErrDebNotFound) {
			return nil, err
		}
		updaterVersion = ""
	}

	req.UpdaterVersion = updaterVersion

	parentId, err := s.metadata.GetParentId()
	if err != nil {
		return nil, err
	}
	req.ParentId = parentId

	instanceId, err := s.metadata.GetInstanceId()
	if err != nil {
		return nil, err
	}
	req.InstanceId = instanceId
	osinfo, err := getOsInfo()
	if err != nil {
		return nil, err
	}
	req.OsInfo = osinfo

	if isAgentHealthy {
		req.AgentState = generated.AgentState_STATE_HEALTHY
	} else {
		req.AgentState = generated.AgentState_STATE_ERROR
	}

	return &req, nil

}

func getOsInfo() (*generated.OSInfo, error) {
	osinfo := generated.OSInfo{}
	cmd := exec.Command("lsb_release", "-d")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(output), ":")
	if len(parts) == 0 {
		return nil, errors.New("lsb_release -d output is empty")
	}
	osinfo.Name = strings.TrimSpace(parts[len(parts)-1])
	cmdUname := exec.Command("uname", "-a")
	outputUname, err := cmdUname.Output()
	if err != nil {
		return nil, err
	}
	osinfo.Uname = strings.TrimSpace(string(outputUname))

	cmdArch := exec.Command("uname", "-m")
	outputArch, err := cmdArch.Output()
	if err != nil {
		return nil, err
	}
	osinfo.Architecture = strings.TrimSpace(string(outputArch))
}
