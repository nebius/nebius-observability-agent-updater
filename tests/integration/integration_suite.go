package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	mockServerControlURL = "http://localhost:18080"
	updaterContainerName = "updater-test"
)

// UpdaterSuite provides setup and helpers for updater integration tests.
type UpdaterSuite struct {
	suite.Suite
	dockerClient *client.Client
	containerID  string
}

// SetupSuite runs before all tests in the suite.
func (s *UpdaterSuite) SetupSuite() {
	var err error
	s.dockerClient, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(s.T(), err, "Failed to create Docker client")

	// Stop any leftover containers from previous test runs
	s.T().Log("Stopping any leftover containers from previous runs...")
	_ = s.stopDockerCompose()

	// Build deb package
	s.T().Log("Building deb package...")
	err = s.buildDebPackage()
	require.NoError(s.T(), err, "Failed to build deb package")

	// Build and start services
	s.T().Log("Building and starting services with docker-compose...")
	err = s.startDockerCompose()
	require.NoError(s.T(), err, "Failed to start docker-compose services")

	time.Sleep(5 * time.Second)

	// Find the updater container
	ctx := context.Background()
	containers, err := s.dockerClient.ContainerList(ctx, container.ListOptions{All: true})
	require.NoError(s.T(), err, "Failed to list containers")

	for _, c := range containers {
		for _, name := range c.Names {
			if name == "/"+updaterContainerName {
				s.containerID = c.ID
				s.T().Logf("Found updater container: %s", s.containerID[:12])
				break
			}
		}
		if s.containerID != "" {
			break
		}
	}
	require.NotEmpty(s.T(), s.containerID, "Failed to find updater container")

	// Verify container is running
	inspect, err := s.dockerClient.ContainerInspect(ctx, s.containerID)
	require.NoError(s.T(), err, "Failed to inspect updater container")
	if !inspect.State.Running {
		s.printContainerLogs("Container failed to start")
		require.True(s.T(), inspect.State.Running, "Container should be running")
	}

	// Wait for systemd to be ready
	s.T().Log("Waiting for systemd to be ready...")
	err = s.waitForSystemdReady()
	if err != nil {
		s.printContainerLogs("systemd failed to start")
	}
	require.NoError(s.T(), err, "systemd should be ready")

	// Install updater deb package
	s.T().Log("Installing updater deb package...")
	s.installUpdater()

	// Write test config after dpkg install (dpkg overwrites the config)
	s.T().Log("Writing test config...")
	s.writeTestConfig()

	// Start updater service
	s.T().Log("Starting updater service...")
	s.startUpdater()

	// Wait for updater to be active
	s.T().Log("Checking updater systemd status...")
	err = s.checkServiceActive("nebius_observability_agent_updater")
	if err != nil {
		s.printContainerLogs("Updater service failed to start")
	}
	require.NoError(s.T(), err, "updater service should be active")
}

// TearDownTest runs after each test.
func (s *UpdaterSuite) TearDownTest() {
	if s.T().Failed() && s.containerID != "" {
		s.printContainerLogs("Test failed - printing container logs for debugging")
		s.T().Logf("\n=== CONTAINER KEPT FOR INVESTIGATION ===")
		s.T().Logf("  docker exec -it %s /bin/bash", updaterContainerName)
		s.T().Logf("========================================\n")
	}
}

// TearDownSuite runs after all tests in the suite.
func (s *UpdaterSuite) TearDownSuite() {
	if s.T().Failed() {
		s.T().Log("Test suite failed - keeping containers for investigation")
		s.T().Logf("  docker exec -it %s /bin/bash", updaterContainerName)
		s.T().Log("  cd tests/integration && docker compose down")
		return
	}

	if s.containerID != "" {
		s.printContainerLogs("Final container logs")
	}

	s.T().Log("Stopping docker-compose services...")
	_ = s.stopDockerCompose()

	if s.dockerClient != nil {
		_ = s.dockerClient.Close()
	}
}

// buildDebPackage builds the updater deb package.
func (s *UpdaterSuite) buildDebPackage() error {
	testDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}
	// tests/integration -> project root
	projectRoot := testDir + "/../.."

	// Clean old debs to avoid Dockerfile COPY conflicts
	cleanCmd := exec.CommandContext(context.Background(), "sh", "-c", "rm -f nebius-observability-agent-updater-*.deb")
	cleanCmd.Dir = projectRoot
	_ = cleanCmd.Run()

	cmd := exec.CommandContext(context.Background(), "make", "build-deb")
	cmd.Dir = projectRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "NEBIUS_UPDATER_VERSION=0.0-dev")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to build deb package: %w", err)
	}
	return nil
}

// startDockerCompose starts the docker-compose services.
func (s *UpdaterSuite) startDockerCompose() error {
	testDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}
	projectRoot := testDir + "/../.."

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var cmd *exec.Cmd
	if _, err := exec.LookPath("docker-compose"); err == nil {
		cmd = exec.CommandContext(ctx, "docker-compose",
			"-f", "tests/integration/docker-compose.yml",
			"up", "-d", "--build")
	} else {
		cmd = exec.CommandContext(ctx, "docker", "compose",
			"-f", "tests/integration/docker-compose.yml",
			"up", "-d", "--build")
	}
	cmd.Dir = projectRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		s.T().Logf("Docker-compose stdout:\n%s", stdout.String())
		s.T().Logf("Docker-compose stderr:\n%s", stderr.String())
		return fmt.Errorf("failed to start docker-compose: %w", err)
	}
	return nil
}

// stopDockerCompose stops the docker-compose services.
func (s *UpdaterSuite) stopDockerCompose() error {
	testDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}
	projectRoot := testDir + "/../.."

	var cmd *exec.Cmd
	if _, err := exec.LookPath("docker-compose"); err == nil {
		cmd = exec.CommandContext(context.Background(), "docker-compose",
			"-f", "tests/integration/docker-compose.yml", "down")
	} else {
		cmd = exec.CommandContext(context.Background(), "docker", "compose",
			"-f", "tests/integration/docker-compose.yml", "down")
	}
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to stop docker-compose: %w\nOutput: %s", err, output)
	}
	return nil
}

// waitForSystemdReady waits for systemd to be fully started in the container.
func (s *UpdaterSuite) waitForSystemdReady() error {
	ctx := context.Background()
	time.Sleep(3 * time.Second)

	for i := 0; i < 60; i++ {
		execConfig := container.ExecOptions{
			Cmd:          []string{"systemctl", "is-system-running"},
			AttachStdout: true,
			AttachStderr: true,
		}
		execID, err := s.dockerClient.ContainerExecCreate(ctx, s.containerID, execConfig)
		if err != nil {
			s.T().Logf("  failed to create exec: %v, retrying... (%d/60)", err, i+1)
			time.Sleep(2 * time.Second)
			continue
		}
		resp, err := s.dockerClient.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{})
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var stdout, stderr strings.Builder
		_, err = stdcopy.StdCopy(&stdout, &stderr, resp.Reader)
		resp.Close()
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		state := strings.TrimSpace(stdout.String())
		if state == "running" || state == "degraded" {
			s.T().Logf("systemd is ready (state: %s)", state)
			return nil
		}
		s.T().Logf("  systemd state: %q, retrying... (%d/60)", state, i+1)
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("systemd did not become ready in time")
}

// checkServiceActive waits for a systemd service to become active.
func (s *UpdaterSuite) checkServiceActive(serviceName string) error {
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		execConfig := container.ExecOptions{
			Cmd:          []string{"systemctl", "is-active", serviceName},
			AttachStdout: true,
			AttachStderr: true,
		}
		execID, err := s.dockerClient.ContainerExecCreate(ctx, s.containerID, execConfig)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		resp, err := s.dockerClient.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{})
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var stdout, stderr strings.Builder
		_, err = stdcopy.StdCopy(&stdout, &stderr, resp.Reader)
		resp.Close()
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		status := strings.TrimSpace(stdout.String())
		if status == "active" {
			s.T().Logf("%s is active", serviceName)
			return nil
		}
		s.T().Logf("  %s status: %q (%d/30)", serviceName, status, i+1)
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("%s did not become active within 60 seconds", serviceName)
}

// installUpdater installs the updater deb package in the container.
func (s *UpdaterSuite) installUpdater() {
	cmd := exec.CommandContext(context.Background(), "docker", "exec", s.containerID,
		"dpkg", "-i", "/deb/nebius-observability-agent-updater.deb")
	output, err := cmd.CombinedOutput()
	s.T().Logf("dpkg install output: %s", string(output))
	require.NoError(s.T(), err, "Failed to install updater deb package")
}

// writeTestConfig writes the test config with insecure gRPC and short poll interval.
func (s *UpdaterSuite) writeTestConfig() {
	configContent := `poll_interval: 2s
poll_jitter: 0s
grpc:
  endpoint: ""
  insecure: true
  timeout: 5s
update_repo_script_path: /usr/local/bin/fake-update-repo.sh
`
	writeCmd := fmt.Sprintf("cat > /etc/nebius-observability-agent-updater/config.yaml << 'TESTEOF'\n%sTESTEOF", configContent)
	cmd := exec.CommandContext(context.Background(), "docker", "exec", s.containerID, "sh", "-c", writeCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		s.T().Logf("write config output: %s", string(output))
	}
	require.NoError(s.T(), err, "Failed to write test config")
}

// startUpdater restarts the updater service via systemd.
func (s *UpdaterSuite) startUpdater() {
	cmd := exec.CommandContext(context.Background(), "docker", "exec", s.containerID,
		"systemctl", "restart", "nebius_observability_agent_updater")
	output, err := cmd.CombinedOutput()
	if err != nil {
		s.T().Logf("systemctl restart output: %s", string(output))
	}
	require.NoError(s.T(), err, "Failed to restart updater via systemd")
}

// execInContainer runs a command in the container and returns stdout.
func (s *UpdaterSuite) execInContainer(cmdArgs ...string) (string, error) {
	ctx := context.Background()
	execConfig := container.ExecOptions{
		Cmd:          cmdArgs,
		AttachStdout: true,
		AttachStderr: true,
	}
	execID, err := s.dockerClient.ContainerExecCreate(ctx, s.containerID, execConfig)
	if err != nil {
		return "", fmt.Errorf("create exec: %w", err)
	}
	resp, err := s.dockerClient.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("attach exec: %w", err)
	}
	defer resp.Close()

	var stdout, stderr strings.Builder
	_, err = stdcopy.StdCopy(&stdout, &stderr, resp.Reader)
	if err != nil {
		return "", fmt.Errorf("read output: %w", err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// readFileInContainer reads a file from inside the container.
func (s *UpdaterSuite) readFileInContainer(path string) (string, error) {
	return s.execInContainer("cat", path)
}

// setMockResponse sets the next response the mock gRPC server will return.
func (s *UpdaterSuite) setMockResponse(resp *agentmanager.GetVersionResponse) {
	data, err := protojson.Marshal(resp)
	require.NoError(s.T(), err, "Failed to marshal response")

	httpResp, err := http.Post(mockServerControlURL+"/api/response", "application/json", bytes.NewReader(data))
	require.NoError(s.T(), err, "Failed to set mock response")
	defer httpResp.Body.Close()
	require.Equal(s.T(), http.StatusOK, httpResp.StatusCode, "Failed to set mock response")
}

// getLatestRequest returns the latest GetVersionRequest received by the mock server.
func (s *UpdaterSuite) getLatestRequest() *agentmanager.GetVersionRequest {
	resp, err := http.Get(mockServerControlURL + "/api/request/latest")
	require.NoError(s.T(), err, "Failed to get latest request")
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	require.Equal(s.T(), http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)

	req := &agentmanager.GetVersionRequest{}
	err = protojson.Unmarshal(body, req)
	require.NoError(s.T(), err, "Failed to unmarshal request")
	return req
}

// getMockRequests returns all GetVersionRequests received by the mock server.
func (s *UpdaterSuite) getMockRequests() []*agentmanager.GetVersionRequest {
	resp, err := http.Get(mockServerControlURL + "/api/requests")
	require.NoError(s.T(), err, "Failed to get requests")
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)

	var rawMessages []json.RawMessage
	err = json.Unmarshal(body, &rawMessages)
	require.NoError(s.T(), err)

	var reqs []*agentmanager.GetVersionRequest
	for _, raw := range rawMessages {
		req := &agentmanager.GetVersionRequest{}
		err = protojson.Unmarshal(raw, req)
		require.NoError(s.T(), err)
		reqs = append(reqs, req)
	}
	return reqs
}

// clearMock clears the mock server state.
func (s *UpdaterSuite) clearMock() {
	resp, err := http.Post(mockServerControlURL+"/api/clear", "application/json", nil)
	require.NoError(s.T(), err, "Failed to clear mock")
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusOK, resp.StatusCode)
}

// dockerExec runs a command in the container using the docker CLI (not the SDK).
// This ensures files are written in the same filesystem context as systemd services.
func (s *UpdaterSuite) dockerExec(args ...string) (string, error) {
	cmdArgs := append([]string{"exec", s.containerID}, args...)
	cmd := exec.CommandContext(context.Background(), "docker", cmdArgs...)
	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

// fakeSystemUptime creates a /fake_proc directory with plain files so that
// gopsutil (via HOST_PROC=/fake_proc) sees a fake boot time.
//
// Files created:
//   - /fake_proc/stat        — copy of /proc/stat with modified btime
//   - /fake_proc/uptime      — fake uptime matching the fake boot time
//   - /fake_proc/<PID>/stat  — copies of /proc/<PID>/stat with adjusted starttime
//
// The starttime field (field 22 in /proc/<pid>/stat) is in clock ticks since
// boot. When we change btime, we must also adjust starttime so that gopsutil's
// CreateTime calculation (btime + starttime/CLK_TCK) still returns the correct
// wall-clock time. Formula: adjusted = real_starttime - (fake_btime - real_btime) * CLK_TCK
//
// A systemd drop-in sets HOST_PROC=/fake_proc for the updater service.
// The updater must be stopped before calling this, then started again.
// Returns a cleanup function.
func (s *UpdaterSuite) fakeSystemUptime(uptimeDuration time.Duration) func() {
	btime := time.Now().Add(-uptimeDuration).Unix()
	uptimeSeconds := int(uptimeDuration.Seconds())

	// The script:
	// 1. Creates /fake_proc/stat with the adjusted btime
	// 2. Creates /fake_proc/uptime with the fake uptime
	// 3. For each PID, copies /proc/<pid>/stat but adjusts field 22 (starttime)
	//    so that CreateTime = btime + starttime/CLK_TCK stays correct
	// 4. Creates a systemd drop-in for HOST_PROC=/fake_proc
	script := fmt.Sprintf(
		`set -e && `+
			`rm -rf /fake_proc && mkdir -p /fake_proc && `+
			`REAL_BTIME=$(grep '^btime' /proc/stat | awk '{print $2}') && `+
			`FAKE_BTIME=%d && `+
			`CLK_TCK=$(getconf CLK_TCK) && `+
			`OFFSET=$(( (FAKE_BTIME - REAL_BTIME) * CLK_TCK )) && `+
			`sed "s/^btime .*/btime $FAKE_BTIME/" /proc/stat > /fake_proc/stat && `+
			`echo "%d.00 0.00" > /fake_proc/uptime && `+
			`for pid in $(ls -d /proc/[0-9]* 2>/dev/null | xargs -n1 basename); do `+
			`  if [ -f /proc/$pid/stat ]; then `+
			`    mkdir -p /fake_proc/$pid && `+
			`    awk -v offset=$OFFSET '{ `+
			`      idx = index($0, ") "); `+
			`      if (idx == 0) { print; next } `+
			`      prefix = substr($0, 1, idx + 1); `+
			`      after = substr($0, idx + 2); `+
			`      n = split(after, f, " "); `+
			`      if (n >= 20) { `+
			`        adj = f[20] - offset; `+
			`        if (adj < 1) adj = 1; `+
			`        f[20] = adj `+
			`      } `+
			`      printf "%%s", prefix; `+
			`      for (i = 1; i <= n; i++) { `+
			`        if (i > 1) printf " "; `+
			`        printf "%%s", f[i] `+
			`      } `+
			`      printf "\n" `+
			`    }' /proc/$pid/stat > /fake_proc/$pid/stat 2>/dev/null || true; `+
			`  fi; `+
			`done && `+
			`mkdir -p /etc/systemd/system/nebius_observability_agent_updater.service.d && `+
			`printf '[Service]\nEnvironment="HOST_PROC=/fake_proc"\n' > /etc/systemd/system/nebius_observability_agent_updater.service.d/fake-uptime.conf && `+
			`systemctl daemon-reload`,
		btime, uptimeSeconds)
	out, err := s.dockerExec("sh", "-c", script)
	if err != nil {
		s.T().Logf("fakeSystemUptime output: %s", out)
	}
	require.NoError(s.T(), err, "Failed to set up fake uptime")

	// Verify btime
	btimeOut, err := s.dockerExec("sh", "-c", "grep ^btime /fake_proc/stat")
	require.NoError(s.T(), err)
	s.T().Logf("Faked system uptime to %s (btime line: %s)", uptimeDuration, btimeOut)

	return func() {
		_, _ = s.dockerExec("sh", "-c",
			"rm -f /etc/systemd/system/nebius_observability_agent_updater.service.d/fake-uptime.conf && "+
				"systemctl daemon-reload && "+
				"rm -rf /fake_proc")
	}
}

// copyProcPid copies /proc/<pid>/stat into /fake_proc/<pid>/stat with adjusted
// starttime so gopsutil can read process info when HOST_PROC=/fake_proc.
// Call this after starting a new process whose uptime needs to be readable.
func (s *UpdaterSuite) copyProcPid(pid string) {
	script := fmt.Sprintf(
		`REAL_BTIME=$(grep '^btime' /proc/stat | awk '{print $2}') && `+
			`FAKE_BTIME=$(grep '^btime' /fake_proc/stat | awk '{print $2}') && `+
			`CLK_TCK=$(getconf CLK_TCK) && `+
			`OFFSET=$(( (FAKE_BTIME - REAL_BTIME) * CLK_TCK )) && `+
			`mkdir -p /fake_proc/%s && `+
			`awk -v offset=$OFFSET '{ `+
			`  idx = index($0, ") "); `+
			`  if (idx == 0) { print; next } `+
			`  prefix = substr($0, 1, idx + 1); `+
			`  after = substr($0, idx + 2); `+
			`  n = split(after, f, " "); `+
			`  if (n >= 20) { `+
			`    adj = f[20] - offset; `+
			`    if (adj < 1) adj = 1; `+
			`    f[20] = adj `+
			`  } `+
			`  printf "%%s", prefix; `+
			`  for (i = 1; i <= n; i++) { `+
			`    if (i > 1) printf " "; `+
			`    printf "%%s", f[i] `+
			`  } `+
			`  printf "\n" `+
			`}' /proc/%s/stat > /fake_proc/%s/stat 2>/dev/null || true`,
		pid, pid, pid)
	_, _ = s.dockerExec("sh", "-c", script)
}

// setupFakeUpdate creates fake scripts for the UPDATE action test:
//  1. /usr/local/bin/fake-update-repo.sh — no-op (replaces real repo updater)
//  2. /usr/local/bin/apt-get — records args to /tmp/apt-get-calls.log then exits 0
//     (shadows real apt-get in PATH; cleaned up after)
//
// Returns a cleanup function that removes the fake apt-get and log file.
func (s *UpdaterSuite) setupFakeUpdate() func() {
	// Create the fake update-repo script (always present via config, harmless)
	_, err := s.dockerExec("sh", "-c", `printf '#!/bin/sh\nexit 0\n' > /usr/local/bin/fake-update-repo.sh && chmod +x /usr/local/bin/fake-update-repo.sh`)
	require.NoError(s.T(), err, "Failed to create fake-update-repo.sh")

	// Create a fake apt-get that logs calls and exits 0
	_, err = s.dockerExec("sh", "-c", `printf '#!/bin/sh\necho "$@" >> /tmp/apt-get-calls.log\nexit 0\n' > /usr/local/bin/apt-get && chmod +x /usr/local/bin/apt-get`)
	require.NoError(s.T(), err, "Failed to create fake apt-get")

	return func() {
		_, _ = s.dockerExec("sh", "-c", "rm -f /usr/local/bin/apt-get /tmp/apt-get-calls.log")
	}
}

// getAptGetCalls reads /tmp/apt-get-calls.log from the container.
func (s *UpdaterSuite) getAptGetCalls() string {
	content, err := s.readFileInContainer("/tmp/apt-get-calls.log")
	if err != nil {
		return ""
	}
	return content
}

// printContainerLogs prints container logs for debugging.
func (s *UpdaterSuite) printContainerLogs(message string) {
	if s.containerID == "" || s.dockerClient == nil {
		return
	}
	s.T().Logf("=== %s ===", message)
	ctx := context.Background()

	// Docker container logs
	s.T().Logf("\n--- Docker Container Logs ---")
	logs, err := s.dockerClient.ContainerLogs(ctx, s.containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       "100",
	})
	if err != nil {
		s.T().Logf("Failed to get container logs: %v", err)
	} else {
		defer logs.Close()
		logData, _ := io.ReadAll(logs)
		s.T().Logf("%s", string(logData))
	}

	// Updater journal logs
	s.T().Logf("\n--- Updater Journal Logs ---")
	s.logContainerExecOutput(ctx, []string{"journalctl", "-u", "nebius_observability_agent_updater", "-n", "200", "--no-pager"})

	// Fake agent journal logs
	s.T().Logf("\n--- Fake Agent Journal Logs ---")
	s.logContainerExecOutput(ctx, []string{"journalctl", "-u", "nebius_observability_agent", "-n", "50", "--no-pager"})

	// Systemd service statuses
	s.T().Logf("\n--- Systemd Status ---")
	s.logContainerExecOutput(ctx, []string{"systemctl", "status", "nebius_observability_agent_updater", "--no-pager"})
	s.logContainerExecOutput(ctx, []string{"systemctl", "status", "nebius_observability_agent", "--no-pager"})

	s.T().Logf("\n=== End of logs ===")
}

// logContainerExecOutput executes a command in the container and logs the output.
func (s *UpdaterSuite) logContainerExecOutput(ctx context.Context, cmd []string) {
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}
	execID, err := s.dockerClient.ContainerExecCreate(ctx, s.containerID, execConfig)
	if err != nil {
		return
	}
	resp, err := s.dockerClient.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{})
	if err != nil {
		return
	}
	defer resp.Close()

	var stdout, stderr strings.Builder
	_, _ = stdcopy.StdCopy(&stdout, &stderr, resp.Reader)
	if stdout.String() != "" {
		s.T().Logf("%s", stdout.String())
	}
}
