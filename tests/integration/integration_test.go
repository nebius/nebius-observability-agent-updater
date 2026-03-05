package integration

import (
	"strings"
	"testing"
	"time"

	"github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestUpdaterSuite(t *testing.T) {
	suite.Run(t, &UpdaterSuite{})
}

func (s *UpdaterSuite) TestUpdaterPolls() {
	s.T().Log("Waiting for updater to poll...")
	time.Sleep(6 * time.Second)

	reqs := s.getMockRequests()
	s.T().Logf("Received %d request(s)", len(reqs))
	require.GreaterOrEqual(s.T(), len(reqs), 1, "Should have received at least 1 request")

	req := reqs[0]
	assert.Equal(s.T(), "test-parent-id", req.GetParentId(), "parent_id should match")
	assert.NotEmpty(s.T(), req.GetInstanceId(), "instance_id should not be empty")
	assert.Equal(s.T(), agentmanager.AgentType_O11Y_AGENT, req.GetType(), "agent_type should be O11Y_AGENT")
}

func (s *UpdaterSuite) TestHealthReporting() {
	s.T().Log("Waiting for updater to poll with health data...")
	time.Sleep(6 * time.Second)

	req := s.getLatestRequest()
	require.NotNil(s.T(), req, "Should have received at least one request")

	assert.Equal(s.T(), agentmanager.AgentState_STATE_HEALTHY, req.GetAgentState(), "agent_state should be HEALTHY")
	require.NotNil(s.T(), req.GetModulesHealth(), "modules_health should be present")
	require.NotNil(s.T(), req.GetModulesHealth().GetProcess(), "modules_health.process should be present")
	assert.Equal(s.T(), agentmanager.AgentState_STATE_HEALTHY, req.GetModulesHealth().GetProcess().GetState(), "process module should be HEALTHY")
}

func (s *UpdaterSuite) TestFeatureFlagsWritten() {
	s.clearMock()

	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_NOP,
		FeatureFlags: map[string]string{
			"TEST_FLAG": "true",
		},
	})

	s.T().Log("Waiting for updater to poll and write feature flags...")
	time.Sleep(8 * time.Second)

	content, err := s.readFileInContainer("/etc/nebius-observability-agent/environment")
	require.NoError(s.T(), err, "Should be able to read environment file")
	s.T().Logf("Environment file content:\n%s", content)

	assert.Contains(s.T(), content, "TEST_FLAG=true", "Environment file should contain TEST_FLAG=true")
}

// Uses RESTART action to bypass the 15-min uptime gate for feature-flag restarts.
func (s *UpdaterSuite) TestFeatureFlagsRestartAgent() {
	pidBefore := s.getAgentPID()
	s.T().Logf("Fake-agent PID before: %s", pidBefore)
	require.NotEqual(s.T(), "0", pidBefore, "Fake-agent should be running")

	s.clearMock()

	flagValue := "value_" + time.Now().Format("150405")
	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_RESTART,
		FeatureFlags: map[string]string{
			"RESTART_TEST_FLAG": flagValue,
		},
	})

	s.T().Log("Waiting for updater to poll, write flags, and restart agent...")
	time.Sleep(8 * time.Second)

	content, err := s.readFileInContainer("/etc/nebius-observability-agent/environment")
	require.NoError(s.T(), err, "Should be able to read environment file")
	assert.Contains(s.T(), content, "RESTART_TEST_FLAG="+flagValue, "Environment file should contain the new flag")

	pidAfter := s.getAgentPID()
	s.T().Logf("Fake-agent PID after: %s", pidAfter)
	require.NotEqual(s.T(), "0", pidAfter, "Fake-agent should still be running")

	assert.NotEqual(s.T(), pidBefore, pidAfter, "Fake-agent PID should have changed (restarted)")

	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_NOP,
		FeatureFlags: map[string]string{
			"RESTART_TEST_FLAG": flagValue,
		},
	})
}

func (s *UpdaterSuite) TestRestartAction() {
	pidBefore := s.getAgentPID()
	s.T().Logf("Fake-agent PID before: %s", pidBefore)
	require.NotEqual(s.T(), "0", pidBefore, "Fake-agent should be running")

	s.clearMock()

	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_RESTART,
	})

	s.T().Log("Waiting for updater to poll and restart agent...")
	time.Sleep(8 * time.Second)

	pidAfter := s.getAgentPID()
	s.T().Logf("Fake-agent PID after: %s", pidAfter)
	require.NotEqual(s.T(), "0", pidAfter, "Fake-agent should still be running")

	assert.NotEqual(s.T(), pidBefore, pidAfter, "Fake-agent PID should have changed (restarted)")

	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_NOP,
	})
}

func (s *UpdaterSuite) TestFreshBootFeatureFlagsRestart() {
	_, err := s.execInContainer("systemctl", "stop", "nebius_observability_agent_updater")
	require.NoError(s.T(), err)

	restore := s.fakeSystemUptime(60 * time.Second)
	defer restore()

	s.clearMock()
	flagValue := "freshboot_" + time.Now().Format("150405")
	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_NOP,
		FeatureFlags: map[string]string{
			"FRESH_BOOT_FLAG": flagValue,
		},
	})

	pidBefore := s.getAgentPID()
	s.T().Logf("Fake-agent PID before: %s", pidBefore)
	require.NotEqual(s.T(), "0", pidBefore, "Fake-agent should be running")

	_, err = s.execInContainer("systemctl", "start", "nebius_observability_agent_updater")
	require.NoError(s.T(), err)

	time.Sleep(1 * time.Second)
	updaterPid, _ := s.execInContainer("sh", "-c", "systemctl show -p MainPID nebius_observability_agent_updater | cut -d= -f2")
	if updaterPid != "" && updaterPid != "0" {
		s.copyProcPid(updaterPid)
	}

	s.T().Log("Waiting for updater to poll and restart agent on fresh boot...")
	time.Sleep(8 * time.Second)

	content, err := s.readFileInContainer("/etc/nebius-observability-agent/environment")
	require.NoError(s.T(), err, "Should be able to read environment file")
	assert.Contains(s.T(), content, "FRESH_BOOT_FLAG="+flagValue, "Environment file should contain the fresh boot flag")

	pidAfter := s.getAgentPID()
	s.T().Logf("Fake-agent PID after: %s", pidAfter)
	require.NotEqual(s.T(), "0", pidAfter, "Fake-agent should still be running")

	assert.NotEqual(s.T(), pidBefore, pidAfter, "Fake-agent PID should have changed (fresh boot immediate restart)")

	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_NOP,
		FeatureFlags: map[string]string{
			"FRESH_BOOT_FLAG": flagValue,
		},
	})
}

func (s *UpdaterSuite) TestFeatureFlagRemoval() {
	s.clearMock()

	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_NOP,
		FeatureFlags: map[string]string{
			"REMOVE_FLAG_A": "1",
			"REMOVE_FLAG_B": "2",
		},
	})

	s.T().Log("Waiting for both flags to be written...")
	time.Sleep(8 * time.Second)

	content, err := s.readFileInContainer("/etc/nebius-observability-agent/environment")
	require.NoError(s.T(), err)
	assert.Contains(s.T(), content, "REMOVE_FLAG_A=1")
	assert.Contains(s.T(), content, "REMOVE_FLAG_B=2")

	s.clearMock()
	pidBefore := s.getAgentPID()
	require.NotEqual(s.T(), "0", pidBefore)

	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_RESTART,
		FeatureFlags: map[string]string{
			"REMOVE_FLAG_A": "1",
		},
	})

	s.T().Log("Waiting for flag removal and restart...")
	time.Sleep(8 * time.Second)

	content, err = s.readFileInContainer("/etc/nebius-observability-agent/environment")
	require.NoError(s.T(), err)
	assert.Contains(s.T(), content, "REMOVE_FLAG_A=1")
	assert.NotContains(s.T(), content, "REMOVE_FLAG_B")

	pidAfter := s.getAgentPID()
	require.NotEqual(s.T(), "0", pidAfter)
	assert.NotEqual(s.T(), pidBefore, pidAfter, "Agent should have restarted after flag removal")

	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_NOP,
		FeatureFlags: map[string]string{
			"REMOVE_FLAG_A": "1",
		},
	})
}

func (s *UpdaterSuite) TestFeatureFlagValidation() {
	s.clearMock()

	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_NOP,
		FeatureFlags: map[string]string{
			"VALID_KEY": "ok",
			"BAD KEY":   "spaces",
			"1DIGIT":    "bad",
			"NL_VAL":    "a\nb",
		},
	})

	s.T().Log("Waiting for updater to process feature flags...")
	time.Sleep(8 * time.Second)

	content, err := s.readFileInContainer("/etc/nebius-observability-agent/environment")
	require.NoError(s.T(), err)
	s.T().Logf("Environment file content:\n%s", content)

	assert.Contains(s.T(), content, "VALID_KEY=ok")
	assert.NotContains(s.T(), content, "BAD KEY")
	assert.NotContains(s.T(), content, "1DIGIT")
	assert.NotContains(s.T(), content, "NL_VAL")
}

func (s *UpdaterSuite) TestRestartWithFeatureFlagChangeIsStable() {
	s.clearMock()

	pidBefore := s.getAgentPID()
	require.NotEqual(s.T(), "0", pidBefore)

	flagValue := "stable_" + time.Now().Format("150405")
	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_RESTART,
		FeatureFlags: map[string]string{
			"STABLE_FLAG": flagValue,
		},
	})

	s.T().Log("Waiting for restart with new feature flag...")
	time.Sleep(8 * time.Second)

	pidAfterRestart := s.getAgentPID()
	require.NotEqual(s.T(), "0", pidAfterRestart)
	assert.NotEqual(s.T(), pidBefore, pidAfterRestart, "Agent should have restarted")

	content, err := s.readFileInContainer("/etc/nebius-observability-agent/environment")
	require.NoError(s.T(), err)
	assert.Contains(s.T(), content, "STABLE_FLAG="+flagValue)

	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_NOP,
		FeatureFlags: map[string]string{
			"STABLE_FLAG": flagValue,
		},
	})

	s.T().Log("Waiting for two more poll cycles to verify stability...")
	time.Sleep(8 * time.Second)

	pidAfterStable := s.getAgentPID()
	assert.Equal(s.T(), pidAfterRestart, pidAfterStable, "Agent PID should NOT change after switching to NOP with same flags")
}

func (s *UpdaterSuite) TestUpdateAction() {
	cleanup := s.setupFakeUpdate()
	defer cleanup()

	s.clearMock()

	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_UPDATE,
		Response: &agentmanager.GetVersionResponse_Update{
			Update: &agentmanager.UpdateActionParams{
				Version: "2.0.0-test",
			},
		},
	})

	s.T().Log("Waiting for updater to process UPDATE action...")
	time.Sleep(8 * time.Second)

	calls := s.getAptGetCalls()
	s.T().Logf("apt-get calls log:\n%s", calls)
	assert.Contains(s.T(), calls, "install --allow-downgrades -y nebius-observability-agent=2.0.0-test")

	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action: agentmanager.Action_NOP,
	})
}

func (s *UpdaterSuite) TestNopKeepsAgentRunning() {
	s.clearMock()

	currentContent, _ := s.readFileInContainer("/etc/nebius-observability-agent/environment")
	flags := make(map[string]string)
	for _, line := range strings.Split(currentContent, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			flags[parts[0]] = parts[1]
		}
	}

	s.setMockResponse(&agentmanager.GetVersionResponse{
		Action:       agentmanager.Action_NOP,
		FeatureFlags: flags,
	})

	time.Sleep(6 * time.Second)

	pidBefore := s.getAgentPID()
	s.T().Logf("Fake-agent PID before: %s", pidBefore)
	require.NotEqual(s.T(), "0", pidBefore, "Fake-agent should be running")

	s.T().Log("Waiting for 2 poll cycles...")
	time.Sleep(8 * time.Second)

	pidAfter := s.getAgentPID()
	s.T().Logf("Fake-agent PID after: %s", pidAfter)

	assert.Equal(s.T(), pidBefore, pidAfter, "Fake-agent PID should NOT have changed with NOP action")
}
