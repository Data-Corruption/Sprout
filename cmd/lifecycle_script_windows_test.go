package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLifecycleUninstallInputIgnoresConsoleEncoding(t *testing.T) {
	dir := t.TempDir()
	probeSource := filepath.Join(dir, "probe.go")
	// Check every input byte and EOF, not just whether the prompt accepts it.
	const probe = `package main
import ("bytes"; "fmt"; "io"; "os")
func main() {
    input, err := io.ReadAll(os.Stdin)
    if err != nil || !bytes.Equal(input, []byte("y\n")) || len(os.Args) != 2 || os.Args[1] != "uninstall" {
        fmt.Fprintf(os.Stderr, "unexpected stdin %x, args %q, error %v", input, os.Args, err)
        os.Exit(1)
    }
    fmt.Println("Uninstall accepted")
    fmt.Fprintln(os.Stderr, "stderr retained")
    os.Exit(23)
}
`
	if err := os.WriteFile(probeSource, []byte(probe), 0600); err != nil {
		t.Fatal(err)
	}
	probePath := filepath.Join(dir, "uninstall probe.exe")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", probePath, probeSource)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build stdin probe: %v\n%s", err, output)
	}
	const script = `
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$tokens = $null
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($env:SPROUT_E2E_SCRIPT, [ref]$tokens, [ref]$errors)
if ($errors.Count -ne 0) { throw ($errors | Out-String) }
$helper = $ast.Find({ param($node)
    $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
    $node.Name -eq 'Invoke-ConfirmedUninstall'
}, $true)
if ($null -eq $helper) { throw 'Uninstall helper is missing.' }
Invoke-Expression $helper.Extent.Text
foreach ($encoding in @([Text.Encoding]::ASCII, [Text.Encoding]::UTF8, [Text.Encoding]::Unicode)) {
    [Console]::InputEncoding = $encoding
    $OutputEncoding = New-Object Text.UTF8Encoding($false)
    $result = Invoke-ConfirmedUninstall -Path $env:SPROUT_E2E_PROBE -Directory $env:SPROUT_E2E_TEMP
    if ($result.ExitCode -ne 23 -or $result.Detail -notmatch 'Uninstall accepted' -or
        $result.Detail -notmatch 'stderr retained') {
        throw "stdin or output changed with $($encoding.WebName): $($result | Out-String)"
    }
}
`
	scriptPath := filepath.Join(dir, "check.ps1")
	if err := os.WriteFile(scriptPath, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"file", "actions-command"} {
		t.Run(mode, func(t *testing.T) {
			args := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass"}
			if mode == "file" {
				args = append(args, "-File", scriptPath)
			} else {
				args = append(args, "-Command", ". '"+strings.ReplaceAll(scriptPath, "'", "''")+"'")
			}
			cmd := exec.CommandContext(ctx, "powershell.exe", args...)
			cmd.Env = append(os.Environ(),
				"SPROUT_E2E_SCRIPT="+filepath.Join(repoRoot(t), "scripts", "test-lifecycle-e2e.ps1"),
				"SPROUT_E2E_PROBE="+probePath,
				"SPROUT_E2E_TEMP="+dir,
			)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("Windows PowerShell stdin regression: %v\n%s", err, output)
			}
		})
	}
}
