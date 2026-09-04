//go:build !windows

package maintenance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sprout/internal/layout"
)

func TestUseSystemdMaintenanceLauncherRequiresManagedCaller(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if useSystemdMaintenanceLauncher() {
		t.Fatal("unmanaged caller selected systemd-run")
	}
	// --- BEGIN service ---
	t.Setenv("NOTIFY_SOCKET", "/run/user/1000/notify")
	if !useSystemdMaintenanceLauncher() {
		t.Fatal("managed, service-capable caller did not select systemd-run")
	}
	// --- END service ---
}

func TestResolveCosignUnixPrefersManagedCopy(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	managed := filepath.Join(home, ".local", "bin", "cosign")
	if err := os.MkdirAll(filepath.Dir(managed), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("managed"), 0o700); err != nil {
		t.Fatal(err)
	}
	ambientDir := filepath.Join(root, "ambient")
	if err := os.Mkdir(ambientDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ambientDir, "cosign"), []byte("ambient"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", ambientDir)

	got, err := resolveCosignUnix()
	if err != nil {
		t.Fatal(err)
	}
	if got != managed {
		t.Fatalf("resolveCosignUnix() = %q, want managed copy %q", got, managed)
	}
}

func TestUnixMaintenanceRunnerIsValidAndCarriesExpectations(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	cosign := filepath.Join(root, "cosign")
	if err := os.WriteFile(cosign, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))

	l := layout.FromStorage(filepath.Join(root, "storage"), "sprout")
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	jobDir := filepath.Join(l.Jobs, "job")
	if err := os.Mkdir(jobDir, 0o700); err != nil {
		t.Fatal(err)
	}
	runner, err := writePlatformRunner(launchJob{
		action:        ActionUpdate,
		id:            "job",
		jobDir:        jobDir,
		layout:        l,
		name:          "sprout",
		releaseURL:    "https://releases.example/",
		certIdentity:  "identity",
		oidcIssuer:    "issuer",
		expectEpoch:   "epoch-1",
		expectVersion: "v1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/bin/sh", "-n", runner).CombinedOutput(); err != nil {
		t.Fatalf("runner syntax: %v: %s", err, output)
	}
	data, err := os.ReadFile(runner)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"trap cleanup EXIT\n",
		`printf '%s\n' "$$" > "$job/` + jobIdentityName + `" || exit 1`,
		"APP_MAINTENANCE_EXPECT_EPOCH='epoch-1'",
		"APP_MAINTENANCE_EXPECT_VERSION='v1.2.3'",
		"curl --connect-timeout 15 --max-time 300",
		"cp '" + l.CachedInstaller + "' \"$cached_installer\"",
		"verify-blob --bundle \"$cached_bundle\"",
		"sh \"$selected\" --update",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("runner missing %q", want)
		}
	}
	// The identity must be published after the traps are armed so a failed
	// write still exits through cleanup.
	if strings.Index(text, "trap cleanup EXIT") > strings.Index(text, `"$job/`+jobIdentityName+`"`) {
		t.Error("runner publishes its identity before arming the cleanup trap")
	}
}
