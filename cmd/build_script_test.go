package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckVarAcceptsEmptyString(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash unavailable: %v", err)
	}

	commonScript := filepath.Join(repoRoot(t), "scripts", "build", "common.sh")
	script := `set -euo pipefail
source "$1"
BUILD_VARS='{"empty":"","text":"value","enabled":false,"port":0}'
check_var empty ""
check_var text value
check_var enabled false
check_var port 0
`
	command := exec.Command(bash, "-c", script, "check-var-test", commonScript)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("check build variables: %v: %s", err, output)
	}
}

func TestLocalBuildIgnoresCIEnvironment(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash unavailable: %v", err)
	}
	for _, kind := range []string{"dev", "prod", "prod-all"} {
		t.Run(kind, func(t *testing.T) {
			// Stop at compilation so this checks the public build flow without
			// downloading tools or compiling every release target in a unit test.
			script := `set -euo pipefail
source "$1"
OUT_DIR="$2/out"
expected_kind="$3"
build_binaries() {
  [[ "$MODE" == local && "$VERSION" == v0.0.0-dev && "$CERT_IDENTITY" == "" ]]
  [[ "$BUILD_KIND" == "$expected_kind" ]]
  if [[ "$expected_kind" == dev ]]; then
    [[ "$DEV_MODE" == true ]]
  else
    [[ "$DEV_MODE" == false ]]
  fi
  # A local build must not even load the remote publication functions.
  if declare -F resolve_release_policy >/dev/null; then
    exit 1
  fi
}
shift 3
build_main "$@"
`
			args := []string{"-c", script, "local-build-test", filepath.Join(repoRoot(t), "scripts", "build.sh"), t.TempDir(), kind}
			if kind != "dev" {
				args = append(args, "--"+kind)
			}
			command := exec.Command(bash, args...)
			command.Env = append(os.Environ(), "CI=true", "GITHUB_REPOSITORY=")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("local build with CI=true: %v: %s", err, output)
			}
		})
	}
}

func TestCIRequiresExplicitAction(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash unavailable: %v", err)
	}
	for _, args := range [][]string{nil, {"--plan", "--execute"}, {"--prod-all"}, {"--plan", "extra"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			command := exec.Command(bash, append([]string{filepath.Join(repoRoot(t), "scripts", "ci.sh")}, args...)...)
			output, err := command.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 2 || !strings.Contains(string(output), "Usage: ./scripts/ci.sh --plan | --execute") {
				t.Fatalf("expected usage error before release setup, got %v: %s", err, output)
			}
		})
	}
}
