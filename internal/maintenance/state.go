// Package maintenance coordinates application processes with installer-owned
// install, update, and uninstall transactions.
package maintenance

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sprout/internal/layout"

	"golang.org/x/mod/semver"
)

const (
	// NonceEnv is set by the installer only for its authorized migrator.
	NonceEnv = "APP_MAINTENANCE_NONCE"

	maxStateSize = 16 * 1024
	nonceBytes   = 32
)

// Phase is the durable installation lifecycle phase.
type Phase string

const (
	PhaseInstalling   Phase = "installing"
	PhaseUpdating     Phase = "updating"
	PhaseReady        Phase = "ready"
	PhaseUninstalling Phase = "uninstalling"
	PhaseUninstalled  Phase = "uninstalled"
)

// State is the installer-owned durable lifecycle state. InstallationEpoch is
// opaque to Go callers: equality, not its current representation, provides
// protection against delayed jobs from a prior installation lifetime.
type State struct {
	Phase             Phase  `json:"phase"`
	Version           string `json:"version"`
	TargetVersion     string `json:"targetVersion"`
	Nonce             string `json:"nonce"`
	ChangedAt         string `json:"changedAt"`
	InstallationEpoch string `json:"installationEpoch"`
}

// Expectation checks selected state fields. Empty fields are ignored; Phase's
// zero value is likewise ignored.
type Expectation struct {
	Phase             Phase
	Version           string
	TargetVersion     string
	InstallationEpoch string
}

// Check verifies that state still describes the installation a caller
// expected to act on.
func (e Expectation) Check(state State) error {
	if e.Phase != "" && state.Phase != e.Phase {
		return fmt.Errorf("maintenance phase is %q, want %q", state.Phase, e.Phase)
	}
	if e.Version != "" && state.Version != e.Version {
		return fmt.Errorf("installed version is %q, want %q", state.Version, e.Version)
	}
	if e.TargetVersion != "" && state.TargetVersion != e.TargetVersion {
		return fmt.Errorf("target version is %q, want %q", state.TargetVersion, e.TargetVersion)
	}
	if e.InstallationEpoch != "" && state.InstallationEpoch != e.InstallationEpoch {
		return fmt.Errorf("installation epoch changed")
	}
	return nil
}

// ReadState reads, strictly decodes, and validates state.json without
// following a symlink or reparse point.
func ReadState(l layout.Layout) (State, error) {
	file, err := openStateFile(l.State)
	if err != nil {
		return State{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return State{}, fmt.Errorf("inspect maintenance state: %w", err)
	}
	if !info.Mode().IsRegular() {
		return State{}, fmt.Errorf("maintenance state is not a regular file")
	}
	if err := validateStateFile(info); err != nil {
		return State{}, err
	}
	if info.Size() > maxStateSize {
		return State{}, fmt.Errorf("maintenance state exceeds %d bytes", maxStateSize)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxStateSize+1))
	if err != nil {
		return State{}, fmt.Errorf("read maintenance state: %w", err)
	}
	if len(data) > maxStateSize {
		return State{}, fmt.Errorf("maintenance state exceeds %d bytes", maxStateSize)
	}

	var state State
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("decode maintenance state: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return State{}, err
	}
	if err := state.Validate(); err != nil {
		return State{}, fmt.Errorf("validate maintenance state: %w", err)
	}
	return state, nil
}

// WriteState validates and atomically replaces state.json. The control
// directory must already have been prepared by layout.Ensure or the installer.
func WriteState(l layout.Layout, state State) error {
	if err := state.Validate(); err != nil {
		return fmt.Errorf("validate maintenance state: %w", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode maintenance state: %w", err)
	}
	data = append(data, '\n')

	temp, err := os.CreateTemp(l.Control, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary maintenance state: %w", err)
	}
	tempPath := temp.Name()
	keep := false
	defer func() {
		_ = temp.Close()
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("protect temporary maintenance state: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write temporary maintenance state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary maintenance state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary maintenance state: %w", err)
	}
	if err := replaceFile(tempPath, l.State); err != nil {
		return fmt.Errorf("publish maintenance state: %w", err)
	}
	keep = true
	if err := syncDirectory(filepath.Dir(l.State)); err != nil {
		return fmt.Errorf("sync maintenance state directory: %w", err)
	}
	return nil
}

// Validate enforces the on-disk state machine's stable representation.
func (s State) Validate() error {
	switch s.Phase {
	case PhaseInstalling, PhaseUpdating, PhaseReady, PhaseUninstalling, PhaseUninstalled:
	default:
		return fmt.Errorf("unknown phase %q", s.Phase)
	}
	if s.ChangedAt == "" {
		return fmt.Errorf("changedAt is empty")
	}
	if _, err := time.Parse(time.RFC3339Nano, s.ChangedAt); err != nil {
		return fmt.Errorf("changedAt %q is not RFC3339: %w", s.ChangedAt, err)
	}
	if strings.TrimSpace(s.InstallationEpoch) == "" {
		return fmt.Errorf("installationEpoch is empty")
	}
	if len(s.InstallationEpoch) > 256 {
		return fmt.Errorf("installationEpoch is too long")
	}
	if s.Version != "" && !semver.IsValid(s.Version) {
		return fmt.Errorf("version %q is not valid semantic version", s.Version)
	}
	if s.TargetVersion != "" && !semver.IsValid(s.TargetVersion) {
		return fmt.Errorf("targetVersion %q is not valid semantic version", s.TargetVersion)
	}
	if s.Nonce != "" && !validNonce(s.Nonce) {
		return fmt.Errorf("nonce must be %d lowercase hexadecimal characters", nonceBytes*2)
	}

	switch s.Phase {
	case PhaseInstalling:
		if s.TargetVersion == "" || s.Nonce == "" {
			return fmt.Errorf("installing requires targetVersion and nonce")
		}
	case PhaseUpdating:
		if s.Version == "" || s.TargetVersion == "" || s.Nonce == "" {
			return fmt.Errorf("updating requires version, targetVersion, and nonce")
		}
	case PhaseReady:
		if s.Version == "" {
			return fmt.Errorf("ready requires version")
		}
		if s.TargetVersion != "" || s.Nonce != "" {
			return fmt.Errorf("ready must not contain targetVersion or nonce")
		}
	case PhaseUninstalling:
		if s.Version == "" {
			return fmt.Errorf("uninstalling requires version")
		}
		if s.TargetVersion != "" || s.Nonce != "" {
			return fmt.Errorf("uninstalling must not contain targetVersion or nonce")
		}
	case PhaseUninstalled:
		if s.Version != "" || s.TargetVersion != "" || s.Nonce != "" {
			return fmt.Errorf("uninstalled must not contain version, targetVersion, or nonce")
		}
	}
	return nil
}

// EnsureDevReady bootstraps or reconciles the isolated development state.
func EnsureDevReady(l layout.Layout, version string) (State, error) {
	if !semver.IsValid(version) {
		return State{}, fmt.Errorf("development version %q is not valid semantic version", version)
	}
	state, err := ReadState(l)
	if err == nil && state.Phase == PhaseReady && state.Version == version {
		return state, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return State{}, err
	}
	epoch := state.InstallationEpoch
	if errors.Is(err, os.ErrNotExist) || state.Phase == PhaseUninstalled {
		epoch, err = newEpoch()
		if err != nil {
			return State{}, err
		}
	}
	state = State{
		Phase:             PhaseReady,
		Version:           version,
		ChangedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		InstallationEpoch: epoch,
	}
	if err := WriteState(l, state); err != nil {
		return State{}, err
	}
	return state, nil
}

func newEpoch() (string, error) {
	var bytes [nonceBytes]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate installation epoch: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}

func validNonce(value string) bool {
	if len(value) != nonceBytes*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("maintenance state contains multiple JSON values")
	}
	return fmt.Errorf("decode trailing maintenance state data: %w", err)
}
