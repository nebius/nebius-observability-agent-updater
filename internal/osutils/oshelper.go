package osutils

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/process"
)

type OsHelper struct {
}

func NewOsHelper() *OsHelper {
	return &OsHelper{}
}

var ErrDebNotFound = fmt.Errorf("package not found")

func (o OsHelper) GetDebVersion(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "dpkg-query", "-W", "-f=${Version}", name)
	output, err := cmd.Output()

	if err != nil {
		return "", ErrDebNotFound
	}
	return strings.TrimSpace(string(output)), nil
}

func (o OsHelper) getSystemdPid(serviceName string) (int32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "systemctl", "show", "--property=MainPID", "--value", serviceName)
	output, err := cmd.Output()

	if err != nil {
		return 0, fmt.Errorf("failed to get PID of %s: %w", serviceName, err)
	}
	pidStr := strings.TrimSpace(string(output))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse PID: %w", err)
	}
	return int32(pid), nil
}

func (o OsHelper) GetSystemdStatus(serviceName string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "systemctl", "is-active", serviceName)
	output, _ := cmd.Output()

	// ignore errors since systemctl returns non-zero exit code when service is inactive

	result := strings.TrimSpace(string(output))
	return result, nil
}

func (o OsHelper) GetLastLogs(serviceName string, lines int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "journalctl", "-u", serviceName, "--no-pager", fmt.Sprintf("--lines=%d", lines))
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get logs for %s: %w", serviceName, err)
	}
	return string(output), nil
}

func (o OsHelper) GetServiceUptime(serviceName string) (time.Duration, error) {
	pid, err := o.getSystemdPid(serviceName)
	if err != nil {
		return 0, err
	}
	if pid == 0 {
		return 0, nil
	}
	return o.getProcessUptime(pid)
}

func (o OsHelper) getProcessUptime(pid int32) (time.Duration, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return 0, fmt.Errorf("failed to create process object: %w", err)
	}

	// Get the process creation time
	createTime, err := p.CreateTime()
	if err != nil {
		return 0, fmt.Errorf("failed to get process creation time: %w", err)
	}

	// Calculate uptime
	uptime := time.Since(time.Unix(createTime/1000, 0)).Round(time.Second)
	return uptime, nil
}

func (o OsHelper) GetSystemUptime() (time.Duration, error) {
	uptime, err := host.BootTime()
	if err != nil {
		return 0, fmt.Errorf("failed to get system uptime: %w", err)
	}
	return time.Since(time.Unix(int64(uptime), 0)).Round(time.Second), nil
}

func (o OsHelper) GetOsName() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "lsb_release", "-d")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	parts := strings.Split(string(output), ":")
	if len(parts) == 0 {
		return "", errors.New("lsb_release -d output is empty")
	}
	return strings.TrimSpace(parts[len(parts)-1]), nil
}

func (o OsHelper) GetUname() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "uname", "-a")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (o OsHelper) GetArch() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "uname", "-m")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (o OsHelper) InstallPackage(packageName string, version string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "apt-get", "install", "--allow-downgrades", "-y", packageName+"="+version)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to install package %s=%s: %w: %s", packageName, version, err, output)
	}
	return nil
}

func (o OsHelper) UpdateRepo(scriptPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, scriptPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to update repo: %w: %s", err, output)
	}
	return nil
}

func (o OsHelper) GetMk8sClusterId(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

func (o OsHelper) GetDirectorySize(path string) (int64, error) {
	// Validate that path is not empty
	if path == "" {
		return 0, fmt.Errorf("path cannot be empty")
	}

	// Check if directory exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Directory doesn't exist, return 0 size without error
		return 0, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "du", "-sb", path)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to get directory size for %s: %w", path, err)
	}

	// Parse output: "size\tpath"
	fields := strings.Fields(string(output))
	if len(fields) < 1 {
		return 0, fmt.Errorf("unexpected du output format")
	}

	size, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse directory size: %w", err)
	}

	return size, nil
}

func (o OsHelper) GetMountpointSize(path string) (int64, error) {
	// Validate that path is not empty
	if path == "" {
		return 0, fmt.Errorf("path cannot be empty")
	}

	// Check if path exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Path doesn't exist, return 0 size without error
		return 0, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "df", "--output=size", "-B1", path)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to get mountpoint size for %s: %w", path, err)
	}

	// Parse output: first line is header "1B-blocks", second line is the size
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("unexpected df output format")
	}

	size, err := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse mountpoint size: %w", err)
	}

	return size, nil
}
