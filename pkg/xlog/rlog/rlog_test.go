package rlog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"sprout/pkg/xsyscall"
)

// --- constructor -----------------------------------------------------------

// TestNewWriterMissingDirPath verifies that an empty DirPath is rejected.
func TestNewWriterMissingDirPath(t *testing.T) {
	if _, err := NewWriter(Config{}); err == nil {
		t.Fatalf("expected error for empty DirPath, got nil")
	}
}

func TestNewWriterCreatesPrivateFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	w, err := NewWriter(Config{DirPath: dir, MaxBufAge: -1})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat log directory: %v", err)
	}
	fileInfo, err := os.Stat(filepath.Join(dir, "latest.log"))
	if err != nil {
		t.Fatalf("stat latest log: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := dirInfo.Mode().Perm(); got != 0o700 {
			t.Fatalf("log directory mode = %04o, want 0700", got)
		}
		if got := fileInfo.Mode().Perm(); got != 0o600 {
			t.Fatalf("latest log mode = %04o, want 0600", got)
		}
	}
}

// --- buffered writes & flush -----------------------------------------------

func TestWriteFlush(t *testing.T) {
	tempDir := t.TempDir()
	w, err := NewWriter(Config{DirPath: tempDir})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	msg := "Hello, test log!\n"
	if n, err := w.Write([]byte(msg)); err != nil || n != len(msg) {
		t.Fatalf("Write: n=%d err=%v", n, err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(tempDir, "latest.log"))
	if string(got) != msg {
		t.Errorf("log mismatch: got %q want %q", got, msg)
	}
}

func TestCloseDoesNotWaitWithWriterLock(t *testing.T) {
	tempDir := t.TempDir()
	w, err := NewWriter(Config{DirPath: tempDir, MaxBufAge: -1})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := w.Write([]byte("pending\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Model the age-trigger goroutine having committed to a tick just as Close
	// begins. The old implementation held w.mu while waiting, so this Flush
	// could never acquire the mutex and Close deadlocked.
	stop := make(chan struct{})
	w.closeAgeTrigger = stop
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		<-stop
		_ = w.Flush()
	}()

	done := make(chan error, 1)
	go func() {
		done <- w.Close()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked waiting for the flush goroutine")
	}

	got, err := os.ReadFile(filepath.Join(tempDir, "latest.log"))
	if err != nil {
		t.Fatalf("read latest.log: %v", err)
	}
	if string(got) != "pending\n" {
		t.Fatalf("close did not flush buffered data: got %q", got)
	}
}

func TestClosedWriterRejectsOperations(t *testing.T) {
	w, err := NewWriter(Config{DirPath: t.TempDir(), MaxBufAge: -1})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := w.Write([]byte("after close")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Write after Close: got %v, want ErrClosed", err)
	}
	if err := w.Flush(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Flush after Close: got %v, want ErrClosed", err)
	}
	if err := w.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("second Close: got %v, want ErrClosed", err)
	}
}

// --- size-based rotation ----------------------------------------------------

func TestRotation(t *testing.T) {
	tempDir := t.TempDir()
	w, err := NewWriter(Config{DirPath: tempDir, MaxFileSize: 10})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	initial := "abc"    // 3 bytes
	rotate := "defghij" // +7 => 10 bytes total
	_, _ = w.Write([]byte(initial))
	_ = w.Flush()
	_, _ = w.Write([]byte(rotate))
	_ = w.Flush()

	// latest.log must hold only the post-rotation text
	if data, _ := os.ReadFile(filepath.Join(tempDir, "latest.log")); string(data) != rotate {
		t.Errorf("latest.log mismatch")
	}

	// one rotated file must contain the initial payload
	var found bool
	entries, _ := os.ReadDir(tempDir)
	for _, e := range entries {
		if e.Name() == "latest.log" || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		found = true
		if data, _ := os.ReadFile(filepath.Join(tempDir, e.Name())); string(data) != initial {
			t.Errorf("%s mismatch", e.Name())
		}
	}
	if !found {
		t.Errorf("expected a rotated log file")
	}
}

// --- retention ---------------------------------------------------------------

// TestPrune verifies that rotation deletes the oldest rotated files beyond
// MaxLogFiles while keeping latest.log.
func TestPrune(t *testing.T) {
	tempDir := t.TempDir()
	w, err := NewWriter(Config{DirPath: tempDir, MaxFileSize: 10, MaxLogFiles: 2})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	// each flush of 10 bytes triggers a rotation
	for range 5 {
		if _, err := w.Write([]byte("0123456789")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := w.Flush(); err != nil {
			t.Fatalf("Flush: %v", err)
		}
	}

	var rotated int
	entries, _ := os.ReadDir(tempDir)
	for _, e := range entries {
		if e.Name() != "latest.log" && strings.HasSuffix(e.Name(), ".log") {
			rotated++
		}
	}
	if rotated != 2 {
		t.Errorf("rotated files kept: got %d want 2", rotated)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "latest.log")); err != nil {
		t.Errorf("latest.log missing after prune: %v", err)
	}
}

// --- cross-process rotation recovery -----------------------------------------

// TestLatestMissingRecovery simulates the window where another process has
// renamed latest.log but not yet recreated it: the writer must recreate the
// file instead of latching a fatal error.
func TestLatestMissingRecovery(t *testing.T) {
	tempDir := t.TempDir()
	w, err := NewWriter(Config{DirPath: tempDir})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	_, _ = w.Write([]byte("before\n"))
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// simulate another process's rotation rename
	if err := os.Rename(filepath.Join(tempDir, "latest.log"), filepath.Join(tempDir, "20000101-000000.000000.log")); err != nil {
		t.Fatalf("rename: %v", err)
	}

	_, _ = w.Write([]byte("after\n"))
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush after external rotation: %v", err)
	}
	if err := w.Error(); err != nil {
		t.Fatalf("writer latched error: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(tempDir, "latest.log")); string(data) != "after\n" {
		t.Errorf("latest.log mismatch: got %q want %q", data, "after\n")
	}
}

// TestLockTimeoutIsNotSticky holds the cross-process lock so a flush times
// out, then releases it: the failed flush must surface an error but keep the
// buffer and the writer usable.
func TestLockTimeoutIsNotSticky(t *testing.T) {
	saved := fileLockTimeout
	fileLockTimeout = 50 * time.Millisecond
	t.Cleanup(func() { fileLockTimeout = saved })

	tempDir := t.TempDir()
	w, err := NewWriter(Config{DirPath: tempDir, MaxBufAge: -1})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	held, err := xsyscall.AcquireLock(
		context.Background(),
		filepath.Join(tempDir, ".rotate.lock"),
		xsyscall.LockOptions{Mode: xsyscall.ModeExclusive},
	)
	if err != nil {
		t.Fatalf("hold rotate lock: %v", err)
	}

	if _, err := w.Write([]byte("kept\n")); err != nil {
		t.Fatalf("buffered Write: %v", err)
	}
	if err := w.Flush(); !errors.Is(err, xsyscall.ErrLocked) {
		t.Fatalf("Flush under contention: got %v, want ErrLocked", err)
	}
	if err := w.Error(); err != nil {
		t.Fatalf("lock timeout poisoned the writer: %v", err)
	}

	if err := held.Close(); err != nil {
		t.Fatalf("release rotate lock: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush after release: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(tempDir, "latest.log")); string(data) != "kept\n" {
		t.Fatalf("latest.log = %q, want buffered data retained across the failed flush", data)
	}
}

// --- concurrency ------------------------------------------------------------

func TestConcurrentWrites(t *testing.T) {
	tempDir := t.TempDir()
	w, err := NewWriter(Config{DirPath: tempDir})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	const (
		goroutines = 5
		perGo      = 20
	)
	msg := []byte("concurrent\n")

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perGo {
				if _, err := w.Write(msg); err != nil {
					t.Errorf("Write: %v", err)
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}
	wg.Wait()
	_ = w.Flush()

	got, _ := os.ReadFile(filepath.Join(tempDir, "latest.log"))
	want := goroutines * perGo * len(msg)
	if len(got) != want {
		t.Errorf("bytes written: got %d want %d", len(got), want)
	}
}
