// Package xlog provides a small leveled logger built on log/slog. Records are
// written to disk through a buffered rotating writer (sub-package rlog) as
// human-readable key=value lines with source locations, and can be filtered
// by level: debug, info, warn, error, or none.
//
// Every record is tagged with the process ID (useful when multiple instances
// share a log directory). The level can be changed at runtime via SetLevel,
// and Close flushes buffered records to disk.
//
// Usage:
//
//	logger, err := xlog.New("./logs", "debug")
//	if err != nil {
//		log.Fatalf("failed to create logger: %v", err)
//	}
//	defer logger.Close() // ensure logs are flushed
//
//	// Log using methods
//	logger.Info("Application started")
//	logger.Debugf("Configuration value: %s", "some_value")
//
//	// Log using context
//	ctx := xlog.IntoContext(context.Background(), logger)
//	xlog.Info(ctx, "Hello") // no-op if ctx holds no logger
//
//	// Structured logging, or handing to slog-aware libraries
//	logger.Slog().Info("job finished", "job", 42, "took", elapsed)
package xlog

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sprout/pkg/xlog/rlog"
)

var (
	ErrInvalidLogLevel = fmt.Errorf("invalid log level")
	ErrClosed          = fmt.Errorf("logger closed")
)

const ValidLevels = "debug|info|warn|error|none"

// levelNone sits above every real level so "none" disables all output.
const levelNone = slog.LevelError + 128

type Logger struct {
	closeMu sync.Mutex
	closed  atomic.Bool
	level   *slog.LevelVar
	handler slog.Handler
	writer  *rlog.Writer
}

type ctxKey struct{}

func IntoContext(ctx context.Context, logger *Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

func FromContext(ctx context.Context) *Logger {
	if logger, ok := ctx.Value(ctxKey{}).(*Logger); ok {
		return logger
	}
	return nil
}

// New creates a logger writing to dirPath (created if missing) at the given
// level. Levels are: debug, info, warn, error, none (case-insensitive).
func New(dirPath string, level string) (*Logger, error) {
	lvl, err := parseLevel(level)
	if err != nil {
		return nil, err
	}
	writer, err := rlog.NewWriter(rlog.Config{DirPath: dirPath})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize rlog writer in directory '%s': %w", dirPath, err)
	}
	levelVar := new(slog.LevelVar)
	levelVar.Set(lvl)
	handler := slog.NewTextHandler(writer, &slog.HandlerOptions{
		AddSource: true,
		Level:     levelVar,
	}).WithAttrs([]slog.Attr{slog.Int("pid", os.Getpid())})
	return &Logger{level: levelVar, handler: handler, writer: writer}, nil
}

// NormalizeLevel validates a log level and returns its canonical persisted
// form. It is the shared policy used by config, CLI, and the logger itself.
func NormalizeLevel(level string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(level))
	switch normalized {
	case "debug", "info", "warn", "error", "none":
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid log level %q (want %s): %w", level, ValidLevels, ErrInvalidLogLevel)
	}
}

func parseLevel(level string) (slog.Level, error) {
	normalized, err := NormalizeLevel(level)
	if err != nil {
		return 0, err
	}
	switch normalized {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	case "none":
		return levelNone, nil
	}
	return 0, fmt.Errorf("unsupported normalized log level %q: %w", normalized, ErrInvalidLogLevel)
}

// Slog returns a *slog.Logger backed by the same handler, level, and rotating
// writer. Use it for structured logging or to hand to slog-aware libraries.
func (l *Logger) Slog() *slog.Logger {
	return slog.New(l.handler)
}

// enabled reports whether a record at the given level would be written.
// Checked in the exported wrappers so disabled levels skip formatting.
func (l *Logger) enabled(level slog.Level) bool {
	return !l.closed.Load() && l.handler.Enabled(context.Background(), level)
}

// log writes msg at the given level, attributing the record to the caller of
// the exported wrapper. Callers must go through exactly one wrapper so the
// runtime.Callers skip depth (Callers, log, wrapper) stays correct.
func (l *Logger) log(level slog.Level, msg string) {
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:])
	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	if err := l.handler.Handle(context.Background(), r); err != nil {
		fmt.Fprintf(os.Stderr, "xlog: failed to write log entry: %v\n", err)
	}
}

func (l *Logger) Debug(v ...any) {
	if l.enabled(slog.LevelDebug) {
		l.log(slog.LevelDebug, fmt.Sprint(v...))
	}
}

func (l *Logger) Debugf(format string, v ...any) {
	if l.enabled(slog.LevelDebug) {
		l.log(slog.LevelDebug, fmt.Sprintf(format, v...))
	}
}

func (l *Logger) Info(v ...any) {
	if l.enabled(slog.LevelInfo) {
		l.log(slog.LevelInfo, fmt.Sprint(v...))
	}
}

func (l *Logger) Infof(format string, v ...any) {
	if l.enabled(slog.LevelInfo) {
		l.log(slog.LevelInfo, fmt.Sprintf(format, v...))
	}
}

func (l *Logger) Warn(v ...any) {
	if l.enabled(slog.LevelWarn) {
		l.log(slog.LevelWarn, fmt.Sprint(v...))
	}
}

func (l *Logger) Warnf(format string, v ...any) {
	if l.enabled(slog.LevelWarn) {
		l.log(slog.LevelWarn, fmt.Sprintf(format, v...))
	}
}

func (l *Logger) Error(v ...any) {
	if l.enabled(slog.LevelError) {
		l.log(slog.LevelError, fmt.Sprint(v...))
	}
}

func (l *Logger) Errorf(format string, v ...any) {
	if l.enabled(slog.LevelError) {
		l.log(slog.LevelError, fmt.Sprintf(format, v...))
	}
}

// Context variants: no-ops when ctx holds no logger.

func Debug(ctx context.Context, v ...any) {
	if l := FromContext(ctx); l != nil && l.enabled(slog.LevelDebug) {
		l.log(slog.LevelDebug, fmt.Sprint(v...))
	}
}

func Debugf(ctx context.Context, format string, v ...any) {
	if l := FromContext(ctx); l != nil && l.enabled(slog.LevelDebug) {
		l.log(slog.LevelDebug, fmt.Sprintf(format, v...))
	}
}

func Info(ctx context.Context, v ...any) {
	if l := FromContext(ctx); l != nil && l.enabled(slog.LevelInfo) {
		l.log(slog.LevelInfo, fmt.Sprint(v...))
	}
}

func Infof(ctx context.Context, format string, v ...any) {
	if l := FromContext(ctx); l != nil && l.enabled(slog.LevelInfo) {
		l.log(slog.LevelInfo, fmt.Sprintf(format, v...))
	}
}

func Warn(ctx context.Context, v ...any) {
	if l := FromContext(ctx); l != nil && l.enabled(slog.LevelWarn) {
		l.log(slog.LevelWarn, fmt.Sprint(v...))
	}
}

func Warnf(ctx context.Context, format string, v ...any) {
	if l := FromContext(ctx); l != nil && l.enabled(slog.LevelWarn) {
		l.log(slog.LevelWarn, fmt.Sprintf(format, v...))
	}
}

func Error(ctx context.Context, v ...any) {
	if l := FromContext(ctx); l != nil && l.enabled(slog.LevelError) {
		l.log(slog.LevelError, fmt.Sprint(v...))
	}
}

func Errorf(ctx context.Context, format string, v ...any) {
	if l := FromContext(ctx); l != nil && l.enabled(slog.LevelError) {
		l.log(slog.LevelError, fmt.Sprintf(format, v...))
	}
}

// SetLevel sets the minimum log level to output.
// Levels are: debug, info, warn, error, none (case-insensitive).
func (l *Logger) SetLevel(level string) error {
	if l.closed.Load() {
		return ErrClosed
	}
	lvl, err := parseLevel(level)
	if err != nil {
		return err
	}
	l.level.Set(lvl)
	return nil
}

// Flush forces buffered records to disk.
func (l *Logger) Flush() error {
	l.closeMu.Lock()
	defer l.closeMu.Unlock()
	if l.closed.Load() {
		return ErrClosed
	}
	if err := l.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush rlog writer: %w", err)
	}
	return nil
}

// Close flushes and closes the underlying writer. Records logged after Close
// are dropped.
func (l *Logger) Close() error {
	l.closeMu.Lock()
	defer l.closeMu.Unlock()
	if l.closed.Load() {
		return ErrClosed
	}
	l.closed.Store(true)
	if err := l.writer.Close(); err != nil {
		return fmt.Errorf("failed to close rlog writer: %w", err)
	}
	return nil
}
