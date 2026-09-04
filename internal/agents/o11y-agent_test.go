package agents

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nebius/nebius-observability-agent-updater/internal/osutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testGuard() *osutils.FileGuard {
	return osutils.NewFileGuard(osutils.DefaultMaxPendingFileOps)
}

const (
	instanceIdA = "computeinstance-a"
	instanceIdB = "computeinstance-b"
)

type staticInstanceId string

func (s staticInstanceId) GetInstanceId() (string, bool, error) {
	return string(s), false, nil
}

type stubInstanceId struct {
	id       string
	fallback bool
	fail     bool
}

func (s *stubInstanceId) GetInstanceId() (string, bool, error) {
	if s.fail {
		return "", false, errors.New("imds unavailable")
	}
	return s.id, s.fallback, nil
}

func TestO11yagent_ConfigVersionDefaultsToZero(t *testing.T) {
	agent := NewO11yagent(t.TempDir(), discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	assert.Equal(t, uint64(0), agent.GetLastSeenConfigVersion())
}

func TestO11yagent_ConfigVersionPersistsAcrossRestart(t *testing.T) {
	stateDir := t.TempDir()

	agent := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	agent.SetLastSeenConfigVersion(42)
	assert.Equal(t, uint64(42), agent.GetLastSeenConfigVersion())

	// A fresh agent simulates a process restart reading from the same state dir.
	reloaded := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	assert.Equal(t, uint64(42), reloaded.GetLastSeenConfigVersion())
}

func TestO11yagent_MalformedStateFileLoadsZero(t *testing.T) {
	stateDir := t.TempDir()
	agent := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	assert.NoError(t, os.WriteFile(agent.stateFilePath, []byte(`{"config_version":"not-a-number"}`), 0640))

	reloaded := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	assert.Equal(t, uint64(0), reloaded.GetLastSeenConfigVersion())
}

func TestO11yagent_StateWithoutInstanceIdResets(t *testing.T) {
	stateDir := t.TempDir()
	agent := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	assert.NoError(t, os.WriteFile(agent.stateFilePath, []byte(`{"config_version":59}`), 0640))

	reloaded := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	assert.Equal(t, uint64(0), reloaded.GetLastSeenConfigVersion())
}

// Releases before the instance-id binding stored the bare version number.
func TestO11yagent_PlainVersionFileLoadsZero(t *testing.T) {
	stateDir := t.TempDir()
	agent := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	assert.NoError(t, os.WriteFile(agent.stateFilePath, []byte("59"), 0640))

	reloaded := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	assert.Equal(t, uint64(0), reloaded.GetLastSeenConfigVersion())
}

func TestO11yagent_StateFromAnotherInstanceResets(t *testing.T) {
	stateDir := t.TempDir()

	origin := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	origin.SetLastSeenConfigVersion(59)

	clone := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdB))
	assert.Equal(t, uint64(0), clone.GetLastSeenConfigVersion())

	// The discard is persisted, so a restart must not load the foreign state again.
	reloaded := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdB))
	assert.Equal(t, uint64(0), reloaded.lastSeenConfigVersion)
	assert.Equal(t, instanceIdB, reloaded.stateInstanceId)

	clone.SetLastSeenConfigVersion(52)
	reloaded = NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdB))
	assert.Equal(t, uint64(52), reloaded.GetLastSeenConfigVersion())
}

func TestO11yagent_UnresolvableInstanceIdKeepsStateUntilResolved(t *testing.T) {
	stateDir := t.TempDir()

	origin := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	origin.SetLastSeenConfigVersion(42)

	source := &stubInstanceId{id: instanceIdA, fail: true}
	agent := NewO11yagent(stateDir, discardLogger(), testGuard(), source)
	assert.Equal(t, uint64(0), agent.GetLastSeenConfigVersion())

	source.fail = false
	assert.Equal(t, uint64(42), agent.GetLastSeenConfigVersion())
}

func TestO11yagent_FallbackInstanceIdIsNotBound(t *testing.T) {
	stateDir := t.TempDir()

	source := &stubInstanceId{id: instanceIdA, fallback: true}
	agent := NewO11yagent(stateDir, discardLogger(), testGuard(), source)
	agent.SetLastSeenConfigVersion(42)
	assert.Equal(t, uint64(0), agent.GetLastSeenConfigVersion())

	reloaded := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	assert.Equal(t, uint64(0), reloaded.GetLastSeenConfigVersion())

	source.fallback = false
	agent.SetLastSeenConfigVersion(43)
	reloaded = NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	assert.Equal(t, uint64(43), reloaded.GetLastSeenConfigVersion())
}

func TestO11yagent_InstanceIdChangeResetsState(t *testing.T) {
	stateDir := t.TempDir()

	source := &stubInstanceId{id: instanceIdA}
	agent := NewO11yagent(stateDir, discardLogger(), testGuard(), source)
	agent.SetLastSeenConfigVersion(42)
	assert.Equal(t, uint64(42), agent.GetLastSeenConfigVersion())

	source.id = instanceIdB
	assert.Equal(t, uint64(0), agent.GetLastSeenConfigVersion())
}

func TestO11yagent_SetWithUnresolvableInstanceIdNotPersisted(t *testing.T) {
	stateDir := t.TempDir()

	source := &stubInstanceId{id: instanceIdA, fail: true}
	agent := NewO11yagent(stateDir, discardLogger(), testGuard(), source)
	agent.SetLastSeenConfigVersion(59)

	reloaded := NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	assert.Equal(t, uint64(0), reloaded.GetLastSeenConfigVersion())

	source.fail = false
	agent.SetLastSeenConfigVersion(60)
	reloaded = NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	assert.Equal(t, uint64(60), reloaded.GetLastSeenConfigVersion())
}

// TestO11yagent_HungDiskDoesNotBlockStartup simulates an unresponsive mount: a
// FIFO with no writer blocks os.ReadFile in open(2). Construction must time out
// and fall back to 0 instead of hanging.
func TestO11yagent_HungDiskDoesNotBlockStartup(t *testing.T) {
	prev := stateIOTimeout
	stateIOTimeout = 100 * time.Millisecond
	t.Cleanup(func() { stateIOTimeout = prev })

	stateDir := t.TempDir()
	fifoPath := filepath.Join(stateDir, (&O11yagent{}).GetServiceName()+".config-version")
	require.NoError(t, syscall.Mkfifo(fifoPath, 0600))

	done := make(chan *O11yagent, 1)
	go func() {
		done <- NewO11yagent(stateDir, discardLogger(), testGuard(), staticInstanceId(instanceIdA))
	}()

	select {
	case agent := <-done:
		assert.Equal(t, uint64(0), agent.GetLastSeenConfigVersion())
	case <-time.After(time.Second):
		t.Fatal("NewO11yagent hung on unresponsive state file")
	}
}
