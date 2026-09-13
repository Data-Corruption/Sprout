// --- FILE template ---

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run the public entrypoint and real parent/child harnesses. Compilation,
// feature cutting, and Incus are stand-ins: this checks reporting and exit
// propagation without installing software or launching containers.
func TestLifecycleE2EResults(t *testing.T) {
	for _, test := range []struct {
		name    string
		failure string
		args    []string
		counts  string
		rows    int
	}{
		{name: "all_scenarios", counts: "12 passed, 0 failed, 0 not finished", rows: 12},
		{name: "child_failure", failure: "test:no-update", counts: "9 passed, 1 failed, 2 not finished", rows: 12},
		{name: "cleanup_failure", failure: "cleanup:no-service", counts: "10 passed, 1 failed, 1 not finished", rows: 12},
		{name: "missing_child_results", failure: "missing:no-service", counts: "11 passed, 0 failed, 1 not finished", rows: 12},
		{name: "setup_failure", failure: "setup", counts: "0 passed, 0 failed, 12 not finished", rows: 12},
		{name: "interrupted_case", failure: "interrupt:headless", args: []string{"--scenario", "headless", "--distros", "debian", "--no-fakes"}, counts: "0 passed, 0 failed, 1 not finished", rows: 1},
		{name: "harness_cleanup_failure", failure: "cleanup:harness", args: []string{"--scenario", "headless", "--distros", "debian", "--no-fakes"}, counts: "1 passed, 0 failed, 0 not finished", rows: 1},
		{name: "single_scenario", args: []string{"--scenario", "headless", "--distros", "debian", "--no-fakes"}, counts: "1 passed, 0 failed, 0 not finished", rows: 1},
		{name: "supplied_release", args: []string{"--release-dir", "candidate", "--distros", "debian", "--no-fakes"}, counts: "1 passed, 0 failed, 0 not finished", rows: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, env := lifecycleReportingFixture(t)
			command := exec.Command("bash", append([]string{"scripts/test.sh", "-e2e"}, test.args...)...)
			command.Dir = filepath.Join(root, "work")
			command.Env = append(env, "REPORT_TEST_FAILURE="+test.failure)
			outputBytes, err := command.CombinedOutput()
			output := string(outputBytes)
			if (err != nil) != (test.failure != "") {
				t.Fatalf("unexpected exit: %v\n%s", err, output)
			}
			summaryBytes, readErr := os.ReadFile(filepath.Join(root, "logs", "summary.txt"))
			if readErr != nil {
				t.Fatalf("read summary: %v\n%s", readErr, output)
			}
			summary := string(summaryBytes)
			if strings.Count(output, "Linux lifecycle E2E:") != 1 || !strings.HasSuffix(output, summary) {
				t.Fatalf("expected one report at the end of output:\n%s", output)
			}
			wantStatus := "PASSED"
			if test.failure != "" {
				wantStatus = "FAILED"
			}
			if !strings.Contains(summary, "Linux lifecycle E2E: "+wantStatus) || !strings.Contains(summary, test.counts) {
				t.Fatalf("unexpected summary:\n%s", summary)
			}
			rows := make(map[string]string)
			for _, line := range strings.Split(summary, "\n") {
				fields := strings.Fields(line)
				if len(fields) == 3 && (fields[0] == "PASS" || fields[0] == "FAIL" || fields[0] == "INCOMPLETE") {
					rows[fields[1]+"/"+fields[2]] = fields[0]
				} else if len(fields) == 4 && fields[0] == "NOT" && fields[1] == "RUN" {
					rows[fields[2]+"/"+fields[3]] = "NOT RUN"
				}
			}
			if len(rows) != test.rows {
				t.Fatalf("got %d cases, want %d:\n%s", len(rows), test.rows, summary)
			}
			if test.rows == 12 {
				for _, scenario := range []string{"default", "no-update", "no-service", "headless"} {
					if _, ok := rows[scenario+"/debian"]; !ok {
						t.Errorf("missing %s scenario:\n%s", scenario, summary)
					}
				}
			}
		})
	}
}

func lifecycleReportingFixture(t *testing.T) (string, []string) {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	for _, dir := range []string{"work", "bin", "tmp", "logs", "instances", "work/candidate"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.CopyFS(filepath.Join(work, "scripts"), os.DirFS(filepath.Join(repoRoot(t), "scripts"))); err != nil {
		t.Fatal(err)
	}
	buildPath := filepath.Join(work, "scripts", "build.sh")
	build, err := os.ReadFile(buildPath)
	if err != nil {
		t.Fatal(err)
	}
	build = append(build, []byte(`
ensure_embed_placeholders() { :; }
go_build() {
  mkdir -p out
  printf 'fixture binary\n' > "out/linux-$HOST_GOARCH"
}
`)...)
	if err := os.WriteFile(buildPath, build, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"work/scripts/cut": `#!/bin/sh
if [ "$REPORT_TEST_FAILURE" = "missing:${PWD##*/}" ]; then
  printf '#!/bin/sh\nexit 0\n' > scripts/test-lifecycle-e2e.sh
fi
`,
		"bin/id":   "#!/bin/sh\nprintf '0\\n'\n",
		"bin/sudo": "#!/bin/sh\nexit 1\n",
		"bin/rm": `#!/bin/sh
if [ "$REPORT_TEST_FAILURE" = cleanup:harness ]; then
  for arg do
    case "$arg" in
      "$TMPDIR"/tmp.*) echo 'injected harness cleanup failure' >&2; exit 1 ;;
    esac
  done
fi
exec /bin/rm "$@"
`,
		"bin/incus": `#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  info) [[ "$REPORT_TEST_FAILURE" != setup ]] ;;
  init)
    for arg in "$@"; do
      if [[ "$arg" == user.sprout-test-owner=* ]]; then
        printf '%s\n' "${arg#*=}" > "$REPORT_TEST_INSTANCES/$3"
      fi
    done
    ;;
  list) [[ ! -f "$REPORT_TEST_INSTANCES/$2" ]] || printf '%s\n' "$2" ;;
  config)
    if [[ "$2" == get ]]; then cat "$REPORT_TEST_INSTANCES/$3"; fi
    ;;
  exec)
    for arg in "$@"; do
      if [[ "$arg" == TEST_SCENARIO=* && "$REPORT_TEST_FAILURE" == "interrupt:${arg#*=}" ]]; then
        kill -TERM "$PPID"
        exit 143
      fi
      if [[ "$arg" == TEST_SCENARIO=* && "$REPORT_TEST_FAILURE" == "test:${arg#*=}" ]]; then
        echo 'injected container failure' >&2
        exit 1
      fi
    done
    ;;
  delete)
    if [[ "$REPORT_TEST_FAILURE" == cleanup:* && "$2" == *"-${REPORT_TEST_FAILURE#*:}-"* ]]; then
      echo 'injected cleanup failure' >&2
      exit 1
    fi
    rm -f "$REPORT_TEST_INSTANCES/$2"
    ;;
  start) ;;
  *) printf 'unexpected Incus command: %s\n' "$*" >&2; exit 1 ;;
esac
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := append(os.Environ(),
		"PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"),
		"TMPDIR="+filepath.Join(root, "tmp"),
		"SPROUT_LIFECYCLE_E2E_LOG_RUN_DIR="+filepath.Join(root, "logs"),
		"SPROUT_LIFECYCLE_E2E_RESULTS_FILE=",
		"REPORT_TEST_INSTANCES="+filepath.Join(root, "instances"),
		"KEEP_FAILED=false",
	)
	return root, env
}
