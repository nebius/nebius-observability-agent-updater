package application

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
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
	// restartGracePeriod accounts for the delay between writing the environment file
	// and the agent process actually starting after systemctl restart, plus uptime
	// rounding errors (~1s). Without this, the next poll after a write+restart can
	// falsely conclude the file is newer than the agent and trigger a spurious restart.
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

func generateEnvironmentFileContent(featureFlags map[string]string) string {
	var sb strings.Builder
	sb.WriteString("# Configuration file for nebius-observability-agent.\n")
	sb.WriteString("# This file is managed by the agent updater process.\n")
	sb.WriteString("# Variables defined here are loaded as environment variables at agent startup.\n")

	if len(featureFlags) == 0 {
		return sb.String()
	}

	keys := make([]string, 0, len(featureFlags))
	for k := range featureFlags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		sb.WriteString(fmt.Sprintf("%s=%s\n", k, featureFlags[k]))
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
	if err != nil && !os.IsNotExist(err) {
		s.logger.Error("Failed to read environment file", "error", err, "path", envPath)
		return false
	}

	if string(existingContent) != newContent {
		s.logger.Info("Feature flags changed, updating environment file", "agent", agent.GetServiceName(), "path", envPath)
		if err := os.WriteFile(envPath, []byte(newContent), 0640); err != nil {
			s.logger.Error("Failed to write environment file", "error", err, "path", envPath)
			return false
		}
	}

	// Check if agent needs restart: env file was modified after agent started.
	// This covers both the case where we just wrote the file above and the case
	// where a previous run wrote the file but couldn't restart (crash, uptime < 15 min).
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

	if freshBoot {
		// On fresh boot, both the file and agent were just created. Skip the grace
		// period and the 15-minute uptime check so feature flags are applied immediately.
		if !fileInfo.ModTime().After(agentStartTime) {
			return false
		}
	} else {
		// Use a grace period when comparing file mtime to agent start time.
		// When we write the file and restart in the same poll cycle, the file mtime and
		// agent start time are within ~1 second of each other. Uptime rounding (to seconds)
		// can make the calculated start time slightly earlier than the actual start, causing
		// the mtime to falsely appear newer than the agent start on the next poll.
		if !fileInfo.ModTime().After(agentStartTime.Add(restartGracePeriod)) {
			return false
		}

		if agentUptime < MinimalUptimeForUpdate {
			s.logger.Info("Agent uptime is less than 15 minutes, skipping restart after feature flags change",
				"agent_uptime", agentUptime.String(), "system_uptime", systemUptime.String(), "agent", agent.GetServiceName())
			return false
		}
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
