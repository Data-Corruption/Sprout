package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sprout/internal/build"
	"sprout/pkg/xlog"
)

func TestCloseJoinsErrorsInReverseOrderAndCachesResult(t *testing.T) {
	firstErr := errors.New("first cleanup")
	secondErr := errors.New("second cleanup")
	a := New(build.BuildInfo{})
	var order []string
	a.AddCleanup(func() error {
		order = append(order, "first")
		return firstErr
	})
	a.AddCleanup(func() error {
		order = append(order, "second")
		return secondErr
	})

	err := a.Close()
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("Close error = %v, want both cleanup errors", err)
	}
	if want := []string{"second", "first"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("cleanup order = %v, want %v", order, want)
	}
	if second := a.Close(); second != err {
		t.Fatalf("second Close returned %v, want cached %v", second, err)
	}
	if len(order) != 2 {
		t.Fatalf("cleanup ran again: %v", order)
	}
}

func TestCloseLogsCleanupFailureBeforeClosingLogger(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "logs")
	logger, err := xlog.New(logDir, "error")
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("cleanup failed")
	a := New(build.BuildInfo{})
	a.Log = logger
	a.AddCleanup(func() error {
		logger.Error("cleanup still has a live logger")
		return wantErr
	})

	if err := a.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("Close error = %v, want %v", err, wantErr)
	}
	data, err := os.ReadFile(filepath.Join(logDir, "latest.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"cleanup still has a live logger",
		"application cleanup failed",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("log missing %q:\n%s", want, data)
		}
	}
}
