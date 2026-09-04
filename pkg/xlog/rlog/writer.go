// Package rlog offers a small log writer that implements buffered, size-based
// log rotation with optional age-based flushing and retention pruning.
//
// Current extension:
//   - [Writer] implements buffered, size-based log rotation with optional
//     age-based flushing and retention pruning - ideal for long-running
//     services that want durable logs without a full logging framework.
//
// [Writer] usage:
//
//	w, err := rlog.NewWriter(rlog.Config{
//	  DirPath:     "./logs",        // required - will be created if missing
//	  MaxFileSize: 512 << 20,       // 512 MB before rotation (optional)
//	  MaxBufSize:  8 * 1024,        // 8 KB in-memory buffer    (optional)
//	  MaxBufAge:   5 * time.Second, // flush after 5 s        (optional)
//	  MaxLogFiles: 4,               // rotated files to keep  (optional)
//	})
//	if err != nil {
//	  log.Fatalf("rlog: %v", err)
//	}
//	defer w.Close()
//
//	// plain io.Writer usage
//	log.SetOutput(w)
//	log.Println("hello, rotating world")
//
// Manual flush / error check:
//
//	if err := w.Flush(); err != nil {
//	  log.Printf("flush failed: %v", err)
//	}
//	if err := w.Error(); err != nil {
//	  log.Printf("writer is unhealthy: %v", err)
//	}
//
// Internals & caveats:
//   - Rotation renames the active file to a timestamped `<ts>.log` and
//     re-creates `latest.log` atomically. A lightweight file lock serializes
//     each disk write with rotation across processes.
//   - After each rotation, rotated files beyond MaxLogFiles are deleted
//     (oldest first). latest.log itself is never pruned.
//   - A single [Writer] should be used per directory per process; multiple
//     processes may safely share the same directory.
package rlog

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"sprout/pkg/xsyscall"
)

const (
	DefaultMaxFileSize = 256 * 1024 * 1024 // 256 MB
	DefaultMaxBufSize  = 4096              // 4 KB
	DefaultMaxBufAge   = 15 * time.Second  // 15 seconds
	DefaultMaxLogFiles = 8                 // rotated files kept (excluding latest.log)
)

// Each lock covers one write/sync and any rotation it triggers, so a peer
// holding it this long is wedged. Bounding the wait degrades a stuck peer
// into a failed write instead of parking Write forever. The failure is not
// sticky: nothing was written, the buffer is intact, and the peer may
// recover, so the next write or age flush tries again. A variable so tests
// can shorten it.
var fileLockTimeout = 10 * time.Second

var ErrClosed = errors.New("log writer is closed")

type noCopy struct{} // see https://github.com/golang/go/issues/8005#issuecomment-190753527

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// Config holds configuration options for [Writer].
type Config struct {
	DirPath     string        // Directory path where log files are stored. Created if it does not exist.
	MaxFileSize int64         // Soft max size of a log file before rotation occurs. Default is 256 MB.
	MaxBufSize  int           // Soft max size of the buffer before flushing to disk. Default is 4 KB.
	MaxBufAge   time.Duration // Max age of the buffer before flushing to disk. Default is 15 seconds. Negative to disable.
	MaxLogFiles int           // Rotated files kept after each rotation, oldest deleted first. Default is 8. Negative to keep all.
}

// Writer implements [io.Writer] for buffered log writing with automatic file rotation.
//
// Errors from the log file itself (stat, write, sync, rotation) are sticky: no
// further data is accepted and subsequent calls return the same error, as in
// several standard library writers. Failing to acquire the cross-process lock
// is not sticky; the write is refused and the buffer kept, so a later write or
// age-triggered flush retries. Retention pruning never fails the writer.
//
// WARNING: Only a single [Writer] should be used per directory per process. Multiple
// process instances writing to the same directory is fine, Multiple [Writer] instances
// within the same process doing so is not.
type Writer struct {
	noCopy  noCopy
	closeMu sync.Mutex // serializes Close without blocking the flush goroutine
	mu      sync.Mutex
	err     error
	closed  bool
	cfg     Config
	buf     []byte
	file    *os.File
	// closeAgeTrigger is a channel used to clean up the age-triggered flush goroutine.
	closeAgeTrigger chan struct{}
	wg              sync.WaitGroup
}

// NewWriter creates and initializes a new [Writer] for the specified directory.
// Creating the directory if it does not already exist. Additional options can
// be provided to customize the Writer's behavior.
func NewWriter(cfg Config) (*Writer, error) {
	if cfg.DirPath == "" {
		return nil, fmt.Errorf("directory path must be provided")
	}

	writer := &Writer{cfg: cfg}

	// set defaults
	if cfg.MaxFileSize <= 0 {
		writer.cfg.MaxFileSize = DefaultMaxFileSize
	}
	if cfg.MaxBufSize <= 0 {
		writer.cfg.MaxBufSize = DefaultMaxBufSize
	}
	if cfg.MaxBufAge == 0 { // leave neg for disabling
		writer.cfg.MaxBufAge = DefaultMaxBufAge
	}
	if cfg.MaxLogFiles == 0 { // leave neg for keeping all
		writer.cfg.MaxLogFiles = DefaultMaxLogFiles
	}

	// setup buff
	writer.buf = make([]byte, 0, writer.cfg.MaxBufSize)

	// ensure directory exists
	if err := os.MkdirAll(cfg.DirPath, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create log directory '%s': %w", cfg.DirPath, err)
	}

	// open latest log file
	var err error
	if writer.file, err = openLogFile(writer.latestPath()); err != nil {
		return nil, err
	}

	// start goroutine for age triggered flushes
	d := writer.cfg.MaxBufAge
	if d > 0 {
		stop := make(chan struct{}) // chan nonsense to avoid a datarace
		writer.closeAgeTrigger = stop
		writer.wg.Add(1)
		go func(done <-chan struct{}) { // receive-only
			defer writer.wg.Done()
			ticker := time.NewTicker(d)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					// Keep ticking through transient lock failures; stop once
					// the writer is closed or poisoned.
					if err := writer.Flush(); errors.Is(err, ErrClosed) || writer.Error() != nil {
						return
					}
				case <-done:
					return
				}
			}
		}(stop)
	}

	return writer, nil
}

// exported

// Write appends p to [Writer.buf]. If the write would overflow the buffer,
// the current buffer is flushed first. When p itself is ≥ MaxBufSize
// the data is written straight to disk instead of being buffered.
// Returns the length of p on success. Partial writes are not supported.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, ErrClosed
	}
	if w.err != nil {
		return 0, w.err
	}
	pLen := len(p)

	// flush if adding p would overflow
	if len(w.buf)+pLen >= w.cfg.MaxBufSize {
		if err := w.flush(); err != nil {
			return 0, err
		}
	}

	// if p ≥ MaxBufSize, stream it directly
	// to the file to avoid an oversized in-memory allocation
	if pLen >= w.cfg.MaxBufSize {
		lock, err := w.acquireFileLock()
		if err != nil {
			return 0, err
		}
		defer lock.Close()

		// correct any rot drift
		if err := w.ensureCurrentFile(); err != nil {
			w.err = err
			return 0, err
		}

		// rotate if this write would overflow the file.
		fi, err := w.file.Stat()
		if err != nil {
			w.err = fmt.Errorf("stat log file: %w", err)
			return 0, w.err
		}
		if fi.Size()+int64(pLen) >= w.cfg.MaxFileSize {
			if err := w.rotateLocked(); err != nil {
				return 0, err
			}
		}

		if _, err := w.file.Write(p); err != nil {
			w.err = fmt.Errorf("write log file: %w", err)
			return 0, w.err
		}
		if err := w.file.Sync(); err != nil {
			w.err = fmt.Errorf("sync log file: %w", err)
			return 0, w.err
		}
		return pLen, nil
	}

	// normal case
	w.buf = append(w.buf, p...)
	return pLen, nil
}

// Flush appends [Writer.buf] to 'DirPath/latest.log', rotates first if appending
// would result in latest.log exceeding MaxFileSize, then clears [Writer.buf].
// Returns an error if the write, file sync, or rotation fails.
//
// Flushing happens automatically during [Writer.Write] when [Writer.buf] exceeds MaxBufSize.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrClosed
	}
	return w.flush()
}

// Error returns the last error encountered by the Writer.
// If no error has occurred, it returns nil.
func (w *Writer) Error() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Close flushes the Writer, age trigger goroutine, and open file.
// It should be called when the Writer is no longer needed.
func (w *Writer) Close() error {
	// Never hold w.mu while waiting for the age-trigger goroutine: it may
	// already be committed to a tick and blocked trying to acquire w.mu in
	// Flush, which would deadlock with Wait.
	w.closeMu.Lock()
	defer w.closeMu.Unlock()

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return ErrClosed
	}
	w.closed = true
	stop := w.closeAgeTrigger
	w.closeAgeTrigger = nil
	w.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	w.wg.Wait()

	w.mu.Lock()
	defer w.mu.Unlock()

	// Public Flush rejects calls after closing begins, so flush internally.
	// Always attempt to close the file and preserve both errors.
	flushErr := w.flush()
	var closeErr error
	if w.file != nil {
		closeErr = w.file.Close()
		w.file = nil
	}
	return errors.Join(flushErr, closeErr)
}

// internal

// flush appends [Writer.buf] to 'DirPath/latest.log', rotates first if appending
// would result in latest.log exceeding MaxFileSize, then clears [Writer.buf].
// Returns an error if the write, file sync, or rotation fails. Assumes mutex is held by caller.
func (w *Writer) flush() error {
	if w.err != nil {
		return w.err
	}
	if w.file == nil {
		w.err = fmt.Errorf("log file %q is closed", w.latestPath())
		return w.err
	}
	if len(w.buf) == 0 {
		return nil
	}
	lock, err := w.acquireFileLock()
	if err != nil {
		return err
	}
	defer lock.Close()

	// correct any rot drift
	if err := w.ensureCurrentFile(); err != nil {
		w.err = err
		return err
	}
	// determine if the file needs to be rotated.
	fi, err := w.file.Stat()
	if err != nil {
		w.err = fmt.Errorf("failed to stat log file: %w", err)
		return w.err
	}
	if fi.Size()+int64(len(w.buf)) >= w.cfg.MaxFileSize {
		if err := w.rotateLocked(); err != nil {
			return err
		}
	}
	// write the buffer to the file and sync.
	if _, err := w.file.Write(w.buf); err != nil {
		w.err = fmt.Errorf("failed to write to log file: %w", err)
		return w.err
	}
	if err := w.file.Sync(); err != nil {
		w.err = fmt.Errorf("failed to sync log file: %w", err)
		return w.err
	}
	w.buf = w.buf[:0]
	return nil
}

// acquireFileLock serializes the file identity check, optional rotation, and
// write/sync transaction across processes. The caller already holds w.mu.
// Failure here does not poison the writer; see fileLockTimeout.
func (w *Writer) acquireFileLock() (*xsyscall.Lock, error) {
	lock, err := xsyscall.AcquireLock(
		context.Background(),
		filepath.Join(w.cfg.DirPath, ".rotate.lock"),
		xsyscall.LockOptions{Mode: xsyscall.ModeExclusive, Timeout: fileLockTimeout},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire log file lock: %w", err)
	}
	return lock, nil
}

// rotateLocked renames the latest log file to the current timestamp and creates
// a new "latest.log" file for subsequent writes. The caller holds w.mu and the
// cross-process file lock.
func (w *Writer) rotateLocked() error {
	if w.err != nil {
		return w.err
	}

	if w.file != nil {
		if err := w.file.Close(); err != nil {
			w.err = fmt.Errorf("failed to close log file: %w", err)
			return w.err
		}
		w.file = nil
	}
	oldPath := w.latestPath()
	ts := time.Now().Format("20060102-150405.000000") // sub-second in case of high-frequency rotation
	newPath := filepath.Join(w.cfg.DirPath, fmt.Sprintf("%s.log", ts))
	if err := os.Rename(oldPath, newPath); err != nil {
		w.err = fmt.Errorf("failed to rename log file: %w", err)
		return w.err
	}
	file, err := openLogFile(oldPath)
	if err != nil {
		w.err = fmt.Errorf("failed to create new log file: %w", err)
		return w.err
	}
	w.file = file
	w.prune()
	return nil
}

// prune deletes the oldest rotated log files beyond MaxLogFiles. Best-effort:
// failures are noted in the fresh latest.log rather than poisoning the writer,
// since losing all future logging over a cleanup hiccup is the worse trade.
// Assumes the mutex and cross-process file lock are held by the caller.
func (w *Writer) prune() {
	if w.cfg.MaxLogFiles < 0 {
		return
	}
	entries, err := os.ReadDir(w.cfg.DirPath)
	if err != nil {
		fmt.Fprintf(w.file, "rlog: retention scan failed: %v\n", err)
		return
	}
	var rotated []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && name != "latest.log" && filepath.Ext(name) == ".log" {
			rotated = append(rotated, name)
		}
	}
	if len(rotated) <= w.cfg.MaxLogFiles {
		return
	}
	sort.Strings(rotated) // timestamped names sort oldest-first
	for _, name := range rotated[:len(rotated)-w.cfg.MaxLogFiles] {
		if err := os.Remove(filepath.Join(w.cfg.DirPath, name)); err != nil {
			fmt.Fprintf(w.file, "rlog: failed to prune %s: %v\n", name, err)
		}
	}
}

// ensureCurrentFile reopens the latest log file if it was rotated by another
// process. The caller holds the cross-process file lock, so latest.log cannot
// change between this identity check and the following write.
func (w *Writer) ensureCurrentFile() error {
	latestInfo, err := os.Stat(w.latestPath())
	if err == nil {
		currentInfo, err := w.file.Stat()
		if err != nil {
			return err
		}
		if os.SameFile(latestInfo, currentInfo) {
			return nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file, err = openLogFile(w.latestPath())
	return err
}

// latestPath returns the path to the latest log file.
func (w *Writer) latestPath() string {
	return filepath.Join(w.cfg.DirPath, "latest.log")
}
