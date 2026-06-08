package osutils

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestFileGuard_WriteFileAtomic_WritesContent(t *testing.T) {
	g := NewFileGuard(DefaultMaxPendingFileOps)
	path := filepath.Join(t.TempDir(), "state")
	if err := g.WriteFileAtomic(path, []byte("42"), 0640, time.Second); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back failed: %v", err)
	}
	if string(content) != "42" {
		t.Fatalf("got %q, want %q", content, "42")
	}
}

func TestFileGuard_ReadFile_ReadsContent(t *testing.T) {
	g := NewFileGuard(DefaultMaxPendingFileOps)
	path := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(path, []byte("7"), 0640); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	content, err := g.ReadFile(path, time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(content) != "7" {
		t.Fatalf("got %q, want %q", content, "7")
	}
}

func TestFileGuard_ReadFile_PreservesNotExist(t *testing.T) {
	g := NewFileGuard(DefaultMaxPendingFileOps)
	_, err := g.ReadFile(filepath.Join(t.TempDir(), "missing"), time.Second)
	if !os.IsNotExist(err) {
		t.Fatalf("expected IsNotExist error, got %v", err)
	}
}

func TestFileGuard_Stat_PreservesNotExist(t *testing.T) {
	g := NewFileGuard(DefaultMaxPendingFileOps)
	_, err := g.Stat(filepath.Join(t.TempDir(), "missing"), time.Second)
	if !os.IsNotExist(err) {
		t.Fatalf("expected IsNotExist error, got %v", err)
	}
}

// TestFileGuard_ReadFile_TimesOutOnHungFile uses a FIFO with no writer, which
// blocks os.ReadFile in open(2) forever, to prove the read does not hang.
func TestFileGuard_ReadFile_TimesOutOnHungFile(t *testing.T) {
	g := NewFileGuard(DefaultMaxPendingFileOps)
	fifo := makeFifo(t)

	start := time.Now()
	_, err := g.ReadFile(fifo, 100*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > time.Second {
		t.Fatalf("read blocked for %s, expected to return shortly after timeout", elapsed)
	}
}

// TestFileGuard_PanicsWhenLeakCapExceeded saturates the cap with reads against a
// hung FIFO; each leaks a goroutine stuck in open(2), so the next call over the
// cap must panic for a restart.
func TestFileGuard_PanicsWhenLeakCapExceeded(t *testing.T) {
	const maxPending = 3
	g := NewFileGuard(maxPending)
	fifo := makeFifo(t)

	// Leak maxPending goroutines that never complete.
	var wg sync.WaitGroup
	for i := 0; i < maxPending; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = g.ReadFile(fifo, 50*time.Millisecond)
		}()
	}
	wg.Wait()

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when leak cap exceeded")
		}
	}()
	_, _ = g.ReadFile(fifo, 50*time.Millisecond)
}

// TestGetMk8sClusterId_DoesNotHangOnWedgedFile points the read at a writer-less
// FIFO; GetMk8sClusterId must return "" shortly after the timeout, not block,
// and the guard must record the path for later reporting.
func TestGetMk8sClusterId_DoesNotHangOnWedgedFile(t *testing.T) {
	prev := mk8sReadTimeout
	mk8sReadTimeout = 100 * time.Millisecond
	t.Cleanup(func() { mk8sReadTimeout = prev })

	g := NewFileGuard(DefaultMaxPendingFileOps)
	o := NewOsHelper(g)
	fifo := makeFifo(t)

	done := make(chan string, 1)
	go func() { done <- o.GetMk8sClusterId(fifo) }()

	select {
	case got := <-done:
		if got != "" {
			t.Fatalf("expected empty cluster id on timeout, got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("GetMk8sClusterId hung on wedged file")
	}

	if paths := g.DrainTimeouts(); len(paths) != 1 || paths[0] != fifo {
		t.Fatalf("expected DrainTimeouts to report %q, got %v", fifo, paths)
	}
}

func TestFileGuard_DrainTimeouts(t *testing.T) {
	g := NewFileGuard(DefaultMaxPendingFileOps)
	if got := g.DrainTimeouts(); got != nil {
		t.Fatalf("expected nil before any timeout, got %v", got)
	}

	fifo := makeFifo(t)
	_, _ = g.ReadFile(fifo, 50*time.Millisecond)
	_, _ = g.ReadFile(fifo, 50*time.Millisecond) // same path, deduped

	paths := g.DrainTimeouts()
	if len(paths) != 1 || paths[0] != fifo {
		t.Fatalf("expected [%q], got %v", fifo, paths)
	}
	// Draining clears the record.
	if got := g.DrainTimeouts(); got != nil {
		t.Fatalf("expected nil after drain, got %v", got)
	}
}

// makeFifo returns a FIFO path that blocks os.ReadFile in open(2) until a writer
// appears, which never happens here — simulating a wedged mount.
func makeFifo(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hang")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatalf("mkfifo failed: %v", err)
	}
	return path
}
