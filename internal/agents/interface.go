package agents

import (
	"github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"
	"github.com/nebius/nebius-observability-agent-updater/internal/healthcheck"
)

type AgentData interface {
	GetAgentType() agentmanager.AgentType
	GetDebPackageName() string
	GetServiceName() string
	IsAgentHealthy() (bool, healthcheck.Response)
	Update(updateRepoScriptPath string, version string) error
	GetLastUpdateError() error
	Restart() error
}
