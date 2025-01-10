package agents

import "github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"

type AgentData interface {
	GetAgentType() agentmanager.AgentType
	GetDebPackageName() string
	GetServiceName() string
	IsAgentHealthy() (bool, []string)
	Update(updateRepoScriptPath string, version string) error
	GetLastUpdateError() error
	Restart() error
}
