package agents

type O11yagent struct {
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
