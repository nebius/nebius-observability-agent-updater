package agents

import (
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

func TestO11yagent_ConfigVersionDefaultsToZero(t *testing.T) {
	agent := NewO11yagent(t.TempDir(), discardLogger(), testGuard())
	assert.Equal(t, uint64(0), agent.GetLastSeenConfigVersion())
}

func TestO11yagent_ConfigVersionPersistsAcrossRestart(t *testing.T) {
	stateDir := t.TempDir()

	agent := NewO11yagent(stateDir, discardLogger(), testGuard())
	agent.SetLastSeenConfigVersion(42)
	assert.Equal(t, uint64(42), agent.GetLastSeenConfigVersion())

	// A fresh agent simulates a process restart reading from the same state dir.
	reloaded := NewO11yagent(stateDir, discardLogger(), testGuard())
	assert.Equal(t, uint64(42), reloaded.GetLastSeenConfigVersion())
}

func TestO11yagent_MalformedStateFileLoadsZero(t *testing.T) {
	stateDir := t.TempDir()
	agent := NewO11yagent(stateDir, discardLogger(), testGuard())
	assert.NoError(t, os.WriteFile(agent.stateFilePath, []byte("not-a-number"), 0640))

	reloaded := NewO11yagent(stateDir, discardLogger(), testGuard())
	assert.Equal(t, uint64(0), reloaded.GetLastSeenConfigVersion())
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
	go func() { done <- NewO11yagent(stateDir, discardLogger(), testGuard()) }()

	select {
	case agent := <-done:
		assert.Equal(t, uint64(0), agent.GetLastSeenConfigVersion())
	case <-time.After(time.Second):
		t.Fatal("NewO11yagent hung on unresponsive state file")
	}
}
