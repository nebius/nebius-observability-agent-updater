package agents

import (
	"github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"
	"github.com/nebius/nebius-observability-agent-updater/internal/healthcheck"
	"github.com/nebius/nebius-observability-agent-updater/internal/osutils"
)

type O11yagent struct {
	lastUpdateError error
	oh              *osutils.OsHelper
}

func NewO11yagent() *O11yagent {
	return &O11yagent{
		oh: osutils.NewOsHelper(),
	}
}

var _ AgentData = (*O11yagent)(nil)

func (o *O11yagent) GetServiceName() string {
	return "nebius_observability_agent"
}

func (o *O11yagent) GetAgentType() agentmanager.AgentType {
	return agentmanager.AgentType_O11Y_AGENT
}

func (o *O11yagent) GetDebPackageName() string {
	return "nebius-observability-agent"
}

func (o *O11yagent) GetHealthCheckUrl() string {
	return "http://127.0.0.1:54783/health"
}

func (o *O11yagent) GetSystemdServiceName() string {
	return "nebius-observability-agent"
}

func (o *O11yagent) IsAgentHealthy() (isHealthy bool, response healthcheck.Response) {
	return healthcheck.CheckHealthWithReasons(o.GetHealthCheckUrl())
}

func (o *O11yagent) Update(updateRepoScriptPath string, version string) error {
	err := o.oh.UpdateRepo(updateRepoScriptPath)
	if err != nil {
		o.lastUpdateError = err
		return err
	}

	err = o.oh.InstallPackage(o.GetDebPackageName(), version)
	if err != nil {
		o.lastUpdateError = err
		return err
	}
	o.lastUpdateError = nil
	return nil
}

func (o *O11yagent) GetLastUpdateError() error {
	return o.lastUpdateError
}

func (o *O11yagent) Restart() error {
	return nil
}
