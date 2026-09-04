package xlog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sprout/pkg/xlog"
)

func TestNewInvalidLevel(t *testing.T) {
	_, err := xlog.New(t.TempDir(), "bogus")
	if err == nil {
		t.Fatalf("expected error for invalid level")
	}
}

func TestNormalizeLevel(t *testing.T) {
	got, err := xlog.NormalizeLevel(" WARN ")
	if err != nil {
		t.Fatalf("normalize level: %v", err)
	}
	if got != "warn" {
		t.Fatalf("normalized level = %q, want warn", got)
	}
}

func TestIntoFromContextRoundTrip(t *testing.T) {
	l, err := xlog.New(t.TempDir(), "debug")
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	defer l.Close()
	ctx := xlog.IntoContext(context.Background(), l)
	if got := xlog.FromContext(ctx); got != l {
		t.Fatalf("round-trip mismatch: want %p, got %p", l, got)
	}
}

func TestCloseIdempotentAndLocked(t *testing.T) {
	l, _ := xlog.New(t.TempDir(), "info")

	if err := l.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := l.Close(); !errors.Is(err, xlog.ErrClosed) {
		t.Fatalf("second close should return ErrClosed, got %v", err)
	}
}

func TestSetLevelAndFlushAfterClose(t *testing.T) {
	l, _ := xlog.New(t.TempDir(), "warn")
	_ = l.Close()

	if err := l.SetLevel("debug"); !errors.Is(err, xlog.ErrClosed) {
		t.Fatalf("SetLevel after close: want ErrClosed, got %v", err)
	}
	if err := l.Flush(); !errors.Is(err, xlog.ErrClosed) {
		t.Fatalf("Flush after close: want ErrClosed, got %v", err)
	}
}

// TestOutput verifies level filtering, message content, and that records are
// attributed to the calling file (guards the runtime.Callers skip depth).
func TestOutput(t *testing.T) {
	dir := t.TempDir()
	l, err := xlog.New(dir, "info")
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	l.Debug("filtered out")
	l.Infof("hello %d", 42)
	ctx := xlog.IntoContext(context.Background(), l)
	xlog.Warn(ctx, "from context")
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "latest.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	got := string(data)

	if strings.Contains(got, "filtered out") {
		t.Errorf("debug record should have been filtered at info level:\n%s", got)
	}
	for _, want := range []string{`msg="hello 42"`, `msg="from context"`, "level=INFO", "level=WARN", "pid=", "xlog_test.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("log output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "source=") && strings.Contains(got, "xlog.go:") {
		t.Errorf("records attributed to xlog.go instead of the caller:\n%s", got)
	}
}
