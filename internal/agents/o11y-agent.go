package agents

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"
	"github.com/nebius/nebius-observability-agent-updater/internal/healthcheck"
	"github.com/nebius/nebius-observability-agent-updater/internal/osutils"
)

// stateIOTimeout bounds disk access for the persisted config version so an
// unresponsive disk cannot hang updater startup (read, in NewO11yagent) or the
// poll loop (write, in SetLastSeenConfigVersion). A read timeout falls back to
// 0; a write timeout keeps the advanced in-memory value. Either way the server
// re-sends the version. Declared as var so tests can shorten it.
var stateIOTimeout = 5 * time.Second

type instanceIdSource interface {
	GetInstanceId() (string, bool, error)
}

type configVersionState struct {
	ConfigVersion uint64 `json:"config_version"`
	InstanceId    string `json:"instance_id"`
}

type O11yagent struct {
	lastUpdateError       error
	lastSeenConfigVersion uint64
	stateInstanceId       string
	instanceIdSource      instanceIdSource
	stateFilePath         string
	logger                *slog.Logger
	fileGuard             *osutils.FileGuard
	oh                    *osutils.OsHelper
}

func NewO11yagent(stateDir string, logger *slog.Logger, fileGuard *osutils.FileGuard, ids instanceIdSource) *O11yagent {
	o := &O11yagent{
		oh:               osutils.NewOsHelper(fileGuard),
		logger:           logger,
		fileGuard:        fileGuard,
		instanceIdSource: ids,
	}
	o.stateFilePath = filepath.Join(stateDir, o.GetServiceName()+".config-version")
	o.lastSeenConfigVersion, o.stateInstanceId = o.loadState()
	return o
}

func (o *O11yagent) loadState() (uint64, string) {
	content, err := o.fileGuard.ReadFile(o.stateFilePath, stateIOTimeout)
	if err != nil {
		if !os.IsNotExist(err) {
			o.logger.Error("failed to read last seen config version", "error", err, "path", o.stateFilePath)
		}
		return 0, ""
	}
	var state configVersionState
	if err := json.Unmarshal(content, &state); err != nil {
		o.logger.Warn("ignoring malformed last seen config version", "error", err, "path", o.stateFilePath)
		return 0, ""
	}
	if state.InstanceId == "" {
		o.logger.Info("ignoring last seen config version without instance id", "path", o.stateFilePath)
		return 0, ""
	}
	return state.ConfigVersion, state.InstanceId
}

// A fallback id comes from a file that may be part of a cloned disk image, so
// it cannot prove the state belongs to this VM.
func (o *O11yagent) ownInstanceId() string {
	id, isFallback, err := o.instanceIdSource.GetInstanceId()
	if err != nil || isFallback {
		return ""
	}
	return id
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

func (o *O11yagent) GetEnvironmentFilePath() string {
	return "/etc/nebius-observability-agent/environment"
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

func (o *O11yagent) GetLastSeenConfigVersion() uint64 {
	id := o.ownInstanceId()
	if id == "" {
		return 0
	}
	if o.stateInstanceId != id {
		if o.lastSeenConfigVersion != 0 {
			o.logger.Info("discarding last seen config version recorded by another instance",
				"state_instance_id", o.stateInstanceId, "instance_id", id)
			o.persist(0, id)
		}
		o.lastSeenConfigVersion = 0
		o.stateInstanceId = id
	}
	return o.lastSeenConfigVersion
}

func (o *O11yagent) SetLastSeenConfigVersion(version uint64) {
	o.lastSeenConfigVersion = version
	id := o.ownInstanceId()
	if id == "" {
		return
	}
	o.stateInstanceId = id
	o.persist(version, id)
}

func (o *O11yagent) persist(version uint64, id string) {
	content, err := json.Marshal(configVersionState{ConfigVersion: version, InstanceId: id})
	if err == nil {
		err = o.fileGuard.WriteFileAtomic(o.stateFilePath, content, 0640, stateIOTimeout)
	}
	if err != nil {
		o.logger.Warn("failed to persist last seen config version", "error", err, "path", o.stateFilePath)
	}
}

func (o *O11yagent) Restart() error {
	return o.oh.RestartService(o.GetServiceName())
}
