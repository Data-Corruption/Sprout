package rlog

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const rotatingWriterHelperEnv = "SPROUT_RLOG_HELPER"

func TestConcurrentRotatingWriters(t *testing.T) {
	const (
		processCount      = 6
		recordsPerProcess = 40
	)

	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	readyDir := filepath.Join(root, "ready")
	barrier := filepath.Join(root, "start")
	if err := os.Mkdir(readyDir, 0o700); err != nil {
		t.Fatalf("create ready directory: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	type child struct {
		cmd    *exec.Cmd
		output bytes.Buffer
	}
	children := make([]child, processCount)
	for i := range children {
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestConcurrentRotatingWritersHelper$")
		cmd.Env = append(os.Environ(),
			rotatingWriterHelperEnv+"=1",
			"SPROUT_RLOG_DIR="+logDir,
			"SPROUT_RLOG_READY_DIR="+readyDir,
			"SPROUT_RLOG_BARRIER="+barrier,
			"SPROUT_RLOG_CHILD="+strconv.Itoa(i),
			"SPROUT_RLOG_RECORDS="+strconv.Itoa(recordsPerProcess),
		)
		cmd.Stdout = &children[i].output
		cmd.Stderr = &children[i].output
		children[i].cmd = cmd
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatalf("start child %d: %v", i, err)
		}
	}

	for i := range children {
		if err := waitForRlogPath(ctx, filepath.Join(readyDir, strconv.Itoa(i))); err != nil {
			cancel()
			t.Fatalf("wait for child %d readiness: %v", i, err)
		}
	}
	if err := os.WriteFile(barrier, nil, 0o600); err != nil {
		cancel()
		t.Fatalf("release child barrier: %v", err)
	}
	for i := range children {
		if err := children[i].cmd.Wait(); err != nil {
			t.Errorf("child %d failed: %v\n%s", i, err, children[i].output.String())
		}
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("concurrent rotating writers timed out: %v", err)
	}

	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("read log directory: %v", err)
	}
	counts := make(map[string]int, processCount*recordsPerProcess)
	rotated := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		if entry.Name() != "latest.log" {
			rotated++
		}
		data, err := os.ReadFile(filepath.Join(logDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line != "" {
				counts[line]++
			}
		}
	}
	if rotated < 2 {
		t.Fatalf("rotated log count = %d, want repeated rotation", rotated)
	}

	for process := range processCount {
		for sequence := range recordsPerProcess {
			record := rotatingWriterRecord(process, sequence)
			switch counts[record] {
			case 0:
				t.Errorf("missing record %q", record)
			case 1:
				delete(counts, record)
			default:
				t.Errorf("record %q appears %d times", record, counts[record])
				delete(counts, record)
			}
		}
	}
	for record, count := range counts {
		t.Errorf("unexpected record %q appears %d times", record, count)
	}
}

func TestConcurrentRotatingWritersHelper(t *testing.T) {
	if os.Getenv(rotatingWriterHelperEnv) != "1" {
		return
	}

	child, err := strconv.Atoi(os.Getenv("SPROUT_RLOG_CHILD"))
	if err != nil {
		t.Fatalf("parse child index: %v", err)
	}
	recordCount, err := strconv.Atoi(os.Getenv("SPROUT_RLOG_RECORDS"))
	if err != nil {
		t.Fatalf("parse record count: %v", err)
	}
	writer, err := NewWriter(Config{
		DirPath:     os.Getenv("SPROUT_RLOG_DIR"),
		MaxFileSize: 256,
		MaxBufSize:  64,
		MaxBufAge:   -1,
		MaxLogFiles: -1,
	})
	if err != nil {
		t.Fatalf("create writer: %v", err)
	}
	defer func() {
		if writer != nil {
			_ = writer.Close()
		}
	}()

	readyPath := filepath.Join(os.Getenv("SPROUT_RLOG_READY_DIR"), strconv.Itoa(child))
	if err := os.WriteFile(readyPath, nil, 0o600); err != nil {
		t.Fatalf("signal readiness: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := waitForRlogPath(ctx, os.Getenv("SPROUT_RLOG_BARRIER")); err != nil {
		t.Fatal(err)
	}

	for sequence := range recordCount {
		record := rotatingWriterRecord(child, sequence) + "\n"
		if _, err := writer.Write([]byte(record)); err != nil {
			t.Fatalf("write sequence %d: %v", sequence, err)
		}
		if sequence%4 == 0 {
			time.Sleep(time.Duration((child+sequence)%3) * time.Millisecond)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	writer = nil
}

func rotatingWriterRecord(process, sequence int) string {
	record := fmt.Sprintf("process=%02d sequence=%03d", process, sequence)
	if sequence%2 == 0 {
		// Exceed MaxBufSize so the same multiprocess run covers both direct
		// writes and buffered flushes.
		record += fmt.Sprintf(" payload=%064d", 0)
	}
	return record
}

func waitForRlogPath(ctx context.Context, path string) error {
	if path == "" {
		return fmt.Errorf("coordination path is empty")
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat coordination path %q: %w", path, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for coordination path %q: %w", path, ctx.Err())
		case <-ticker.C:
		}
	}
}
