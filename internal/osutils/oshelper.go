package osutils

import (
	"errors"
	"fmt"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/process"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type OsHelper struct {
}

func NewOsHelper() *OsHelper {
	return &OsHelper{}
}

var ErrDebNotFound = fmt.Errorf("package not found")

func (o OsHelper) GetDebVersion(name string) (string, error) {
	cmd := exec.Command("dpkg-query", "-W", "-f=${Version}", name)
	output, err := cmd.Output()

	if err != nil {
		return "", ErrDebNotFound
	}
	return strings.TrimSpace(string(output)), nil
}

func (o OsHelper) getSystemdPid(serviceName string) (int32, error) {
	cmd := exec.Command("systemctl", "show", "--property=MainPID", "--value", serviceName)
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
	cmd := exec.Command("lsb_release", "-d")
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
	cmd := exec.Command("uname", "-a")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (o OsHelper) GetArch() (string, error) {
	cmd := exec.Command("uname", "-m")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (o OsHelper) InstallPackage(packageName string, version string) error {
	cmd := exec.Command("apt-get", "install", "--allow-downgrades", "-y", packageName+"="+version)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to install package %s=%s: %w: %s", packageName, version, err, output)
	}
	return nil
}

func (o OsHelper) UpdateRepo(scriptPath string) error {
	cmd := exec.Command(scriptPath)
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
