package osutils

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/process"
)

type OsHelper struct {
	fileGuard *FileGuard
}

func NewOsHelper(fileGuard *FileGuard) *OsHelper {
	return &OsHelper{fileGuard: fileGuard}
}

// mk8sReadTimeout bounds the mk8s-cluster-id file read in GetMk8sClusterId so an
// unresponsive disk cannot hang the poll loop. Declared as var so tests can
// shorten it.
var mk8sReadTimeout = 5 * time.Second

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

func (o OsHelper) RestartService(serviceName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "systemctl", "restart", serviceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to restart service %s: %w: %s", serviceName, err, output)
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

// GetMk8sClusterId returns the cluster id, or "" if the file is absent or its
// read fails. A read timeout is recorded by the shared FileGuard and surfaced
// to the backend via FileGuard.DrainTimeouts, so it is not returned here.
func (o OsHelper) GetMk8sClusterId(path string) string {
	content, err := o.fileGuard.ReadFile(path, mk8sReadTimeout)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

// DefaultMaxPendingFileOps caps how many file-op goroutines may be in flight
// before FileGuard panics for a restart.
const DefaultMaxPendingFileOps = 100

// FileGuard bounds filesystem syscalls with a timeout so a wedged mount cannot
// hang the caller. Each call runs its syscall in a goroutine counted in
// pending; one that exceeds its timeout keeps running (and stays counted) until
// the syscall finally unblocks. On a wedged mount these accumulate, so when the
// in-flight count exceeds the cap FileGuard panics: the mount is assumed wedged
// and a process restart is the only chance of recovery. A single instance is
// shared across all callers so the cap bounds the whole process, not one call
// site.
type FileGuard struct {
	pending atomic.Int64
	max     int64

	mu sync.Mutex
	// distinct paths that have timed out since the last drain; bounded by the
	// fixed set of guarded call sites, not by call frequency.
	timeouts map[string]struct{}
}

func NewFileGuard(maxPending int) *FileGuard {
	return &FileGuard{max: int64(maxPending), timeouts: make(map[string]struct{})}
}

func (g *FileGuard) recordTimeout(path string) {
	g.mu.Lock()
	g.timeouts[path] = struct{}{}
	g.mu.Unlock()
}

// DrainTimeouts returns the distinct paths whose access timed out since the
// previous call, sorted, and clears the record. Callers use it to report a
// wedged disk to the backend once per cycle rather than per failed read.
func (g *FileGuard) DrainTimeouts() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.timeouts) == 0 {
		return nil
	}
	paths := make([]string, 0, len(g.timeouts))
	for p := range g.timeouts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	g.timeouts = make(map[string]struct{})
	return paths
}

// guarded runs fn in a goroutine and returns either its result or a timeout
// error. A timed-out goroutine keeps running until its syscall unblocks, so it
// stays counted in pending; once the in-flight count exceeds the cap the
// process panics for a restart.
func guarded[T any](g *FileGuard, path string, timeout time.Duration, fn func() (T, error)) (T, error) {
	if inflight := g.pending.Add(1); inflight > g.max {
		g.pending.Add(-1) // this call never spawns its goroutine; don't count it
		panic(fmt.Sprintf("osutils: %d in-flight file-op goroutines exceed cap %d (path=%s)", inflight, g.max, path))
	}
	type result struct {
		val T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer g.pending.Add(-1)
		val, err := fn()
		ch <- result{val: val, err: err}
	}()
	select {
	case res := <-ch:
		return res.val, res.err
	case <-time.After(timeout):
		g.recordTimeout(path)
		var zero T
		return zero, fmt.Errorf("timeout on %s after %s", path, timeout)
	}
}

// ReadFile reads path with a timeout. The returned error preserves os.ReadFile's
// error on the non-timeout path, so os.IsNotExist still works.
func (g *FileGuard) ReadFile(path string, timeout time.Duration) ([]byte, error) {
	return guarded(g, path, timeout, func() ([]byte, error) { return os.ReadFile(path) })
}

// Stat stats path with a timeout, preserving os.Stat's error on the non-timeout
// path so os.IsNotExist still works.
func (g *FileGuard) Stat(path string, timeout time.Duration) (os.FileInfo, error) {
	return guarded(g, path, timeout, func() (os.FileInfo, error) { return os.Stat(path) })
}

// WriteFileAtomic runs the atomic write with a timeout.
func (g *FileGuard) WriteFileAtomic(path string, data []byte, perm os.FileMode, timeout time.Duration) error {
	_, err := guarded(g, path, timeout, func() (struct{}, error) {
		return struct{}{}, WriteFileAtomic(path, data, perm)
	})
	return err
}

// WriteFileAtomic writes data to a temp file in the same directory, fsyncs it,
// then renames it to the target path so readers never see partial content.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
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
