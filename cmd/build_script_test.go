package main

import (
	"os/exec"
	"path/filepath"
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
