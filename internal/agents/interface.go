package agents

import generated "github.com/nebius/nebius-observability-agent-updater/generated/proto"

type AgentData interface {
	GetAgentType() generated.AgentType
	GetDebPackageName() string
	GetServiceName() string
	IsAgentHealthy() (bool, []string)
	Update(updateRepoScriptPath string, version string) error
	GetLastUpdateError() error
	Restart() error
}
