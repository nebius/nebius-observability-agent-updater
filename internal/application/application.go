package application

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"
	"github.com/nebius/nebius-observability-agent-updater/internal/agents"
	"github.com/nebius/nebius-observability-agent-updater/internal/config"
)

var validEnvKeyRegexp = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type App struct {
	config *config.Config
	client updaterClient
	logger *slog.Logger
	agents []agents.AgentData
	oh     oshelper
}

const (
	MinimalUptimeForUpdate = 15 * time.Minute
	// restartGracePeriod prevents spurious restarts when file mtime is close to agent start time.
	restartGracePeriod = 30 * time.Second
)

type updaterClient interface {
	SendAgentData(agent agents.AgentData) (*agentmanager.GetVersionResponse, error)
	Close()
}

type oshelper interface {
	GetSystemUptime() (time.Duration, error)
	GetServiceUptime(serviceName string) (time.Duration, error)
}

func New(config *config.Config, client updaterClient, logger *slog.Logger, agents []agents.AgentData, oh oshelper) *App {
	app := &App{config: config, client: client, logger: logger, agents: agents, oh: oh}
	return app
}

func (s *App) poll(agent agents.AgentData) {
	s.logger.Info("Polling for ", "agent", agent.GetServiceName())
	response, err := s.client.SendAgentData(agent)
	if err != nil {
		s.logger.Error("Failed to send agent data", "error", err, "agent", agent.GetServiceName())
		return
	}
	s.logger.Debug("Received response", "response", response, "agent", agent.GetServiceName())

	restarted := s.processFeatureFlags(response, agent)

	if response.Action == agentmanager.Action_UPDATE {
		s.Update(response, agent)
	}
	if response.Action == agentmanager.Action_RESTART && !restarted {
		s.Restart(agent)
	}
}

func (s *App) Update(response *agentmanager.GetVersionResponse, agent agents.AgentData) {
	systemUptime, err := s.oh.GetSystemUptime()
	if err != nil {
		s.logger.Error("Failed to get system uptime", "error", err)
	} else if systemUptime < MinimalUptimeForUpdate {
		s.logger.Info("System uptime is less than 15 minutes, skipping update", "system_uptime", systemUptime.String())
		return
	}
	updateData := response.GetUpdate()
	if updateData == nil {
		s.logger.Error("Received empty update data")
		return
	}
	s.logger.Info("Updating agent to version", "version", updateData.GetVersion(), "agent", agent.GetServiceName())
	err = agent.Update(s.config.UpdateRepoScriptPath, updateData.GetVersion())
	if err != nil {
		s.logger.Error("Failed to update agent", "error", err)
		return
	}
}

func (s *App) Restart(agent agents.AgentData) {
	s.logger.Info("Restarting agent", "agent", agent.GetServiceName())
	err := agent.Restart()
	if err != nil {
		s.logger.Error("Failed to restart agent", "error", err)
		return
	}
}

// atomicWriteFile writes data to a temp file in the same directory, fsyncs it,
// then renames it to the target path so readers never see partial content.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	defer func() {
		// Clean up temp file on any failure.
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	tmpPath = "" // rename succeeded, nothing to clean up
	return nil
}

// needsQuoting returns true if a value contains characters that require
// double-quoting in a systemd EnvironmentFile (spaces, quotes, backslashes,
// or leading/trailing whitespace).
func needsQuoting(v string) bool {
	if v == "" {
		return false
	}
	if v[0] == ' ' || v[0] == '\t' || v[len(v)-1] == ' ' || v[len(v)-1] == '\t' {
		return true
	}
	return strings.ContainsAny(v, ` "'`+"`"+`\$`)
}

func generateEnvironmentFileContent(featureFlags map[string]string) string {
	var sb strings.Builder
	sb.WriteString("# Managed by agent updater. Variables are loaded as env vars at agent startup.\n")

	if len(featureFlags) == 0 {
		return sb.String()
	}

	keys := make([]string, 0, len(featureFlags))
	for k := range featureFlags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := featureFlags[k]
		if needsQuoting(v) {
			v = `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
		}
		fmt.Fprintf(&sb, "%s=%s\n", k, v)
	}
	return sb.String()
}

func (s *App) validateFeatureFlags(flags map[string]string) map[string]string {
	if len(flags) == 0 {
		return flags
	}
	valid := make(map[string]string, len(flags))
	for k, v := range flags {
		if !validEnvKeyRegexp.MatchString(k) {
			s.logger.Warn("Skipping feature flag with invalid key", "key", k)
			continue
		}
		if strings.ContainsAny(v, "\n\r") {
			s.logger.Warn("Skipping feature flag with newline in value", "key", k)
			continue
		}
		valid[k] = v
	}
	return valid
}

func (s *App) processFeatureFlags(response *agentmanager.GetVersionResponse, agent agents.AgentData) bool {
	envPath := agent.GetEnvironmentFilePath()
	if envPath == "" {
		return false
	}

	featureFlags := s.validateFeatureFlags(response.GetFeatureFlags())
	newContent := generateEnvironmentFileContent(featureFlags)

	existingContent, err := os.ReadFile(envPath)
	fileExists := err == nil
	if err != nil && !os.IsNotExist(err) {
		s.logger.Error("Failed to read environment file", "error", err, "path", envPath)
		return false
	}

	// No flags and no existing file — nothing to do, avoid creating a
	// header-only file that would trigger a spurious restart.
	if len(featureFlags) == 0 && !fileExists {
		return false
	}

	if string(existingContent) != newContent {
		s.logger.Info("Feature flags changed, updating environment file", "agent", agent.GetServiceName(), "path", envPath)
		if err := atomicWriteFile(envPath, []byte(newContent), 0640); err != nil {
			s.logger.Error("Failed to write environment file", "error", err, "path", envPath)
			return false
		}
	}

	fileInfo, err := os.Stat(envPath)
	if err != nil {
		s.logger.Error("Failed to stat environment file", "error", err, "path", envPath)
		return false
	}

	agentUptime, err := s.oh.GetServiceUptime(agent.GetServiceName())
	if err != nil {
		s.logger.Error("Failed to get agent uptime", "error", err)
		return false
	}

	systemUptime, sysErr := s.oh.GetSystemUptime()
	freshBoot := sysErr == nil && systemUptime < MinimalUptimeForUpdate
	agentStartTime := time.Now().Add(-agentUptime)

	gracePeriod := restartGracePeriod
	requireMinUptime := true
	if freshBoot {
		gracePeriod = 0
		requireMinUptime = false
	}

	if !fileInfo.ModTime().After(agentStartTime.Add(gracePeriod)) {
		return false
	}
	if requireMinUptime && agentUptime < MinimalUptimeForUpdate {
		s.logger.Info("Agent uptime is less than 15 minutes, skipping restart after feature flags change",
			"agent_uptime", agentUptime.String(), "system_uptime", systemUptime.String(), "agent", agent.GetServiceName())
		return false
	}

	s.logger.Info("Restarting agent due to feature flags change",
		"agent", agent.GetServiceName(), "agent_uptime", agentUptime.String(), "system_uptime", systemUptime.String())
	if err := agent.Restart(); err != nil {
		s.logger.Error("Failed to restart agent after feature flags change", "error", err, "agent", agent.GetServiceName())
		return false
	}
	return true
}

func (s *App) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for _, agent := range s.agents {
		wg.Add(1)
		go func(a agents.AgentData) {
			defer wg.Done()
			s.runForAgent(ctx, a)
		}(agent)
	}
	wg.Wait()
	return nil
}

func (s *App) runForAgent(ctx context.Context, agent agents.AgentData) {
	for {
		interval := s.config.PollInterval + time.Duration(float64(s.config.PollJitter)*(2*rand.Float64()-1))
		s.logger.Info("Calculated poll interval", "poll_interval", interval.String(), "agent", agent.GetServiceName())
		if interval < 0 {
			interval = 0
		}
		select {
		case <-time.After(interval):
			s.poll(agent)
		case <-ctx.Done():
			return
		}
	}
}

func (s *App) Shutdown() error {
	return nil
}
