package agents

import generated "github.com/nebius/nebius-observability-agent-updater/generated/proto"

type O11yagent struct {
}

func NewO11yagent() *O11yagent {
	return &O11yagent{}
}

var _ AgentData = (*O11yagent)(nil)

func (o *O11yagent) GetServiceName() string {
	return "nebius_observability_agent"
}

func (o *O11yagent) GetAgentType() generated.AgentType {
	return generated.AgentType_O11Y_AGENT
}

func (o *O11yagent) GetDebPackageName() string {
	return "nebius-observability-agent"
}

func (o *O11yagent) GetHealthCheckUrl() string {
	return "http://localhost:8080/health" // FIXME
}

func (o *O11yagent) GetSystemdServiceName() string {
	return "nebius-observability-agent"
}

func (o *O11yagent) IsAgentHealthy() bool {
	return true
}
