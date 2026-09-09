package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryFlagChangesCommandWorkingDirectory(t *testing.T) {
	bd := buildBDForInitTests(t)
	root := t.TempDir()
	callerDir := filepath.Join(root, "caller")
	targetDir := filepath.Join(root, "target")
	for _, dir := range []string{callerDir, targetDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
		}
	}
	runGit(targetDir, "init", "-q")
	runGit(targetDir, "config", "core.hooksPath", ".git/hooks")
	runGit(targetDir, "remote", "add", "origin", "https://example.invalid/target.git")

	beadsDir := filepath.Join(targetDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	for _, tc := range []struct {
		name   string
		target string
	}{
		{name: "absolute", target: targetDir},
		{name: "relative", target: filepath.Join("..", "target")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bd, "--sandbox", "-C", tc.target, "context", "--json")
			cmd.Dir = callerDir
			cmd.Env = directoryFlagTestEnv(t)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("bd -C %s context --json: %v\nstdout:\n%s\nstderr:\n%s", tc.target, err, stdout.String(), stderr.String())
			}

			var got ContextInfo
			start := bytes.IndexByte(stdout.Bytes(), '{')
			if start < 0 || json.Unmarshal(stdout.Bytes()[start:], &got) != nil {
				t.Fatalf("parse context JSON:\n%s", stdout.String())
			}
			want, err := filepath.EvalSymlinks(targetDir)
			if err != nil {
				t.Fatalf("EvalSymlinks target: %v", err)
			}
			if got.CWDRepoRoot != want {
				t.Fatalf("cwd_repo_root = %q, want -C target repo %q; full context: %+v", got.CWDRepoRoot, want, got)
			}
		})
	}
}

func directoryFlagTestEnv(t *testing.T) []string {
	t.Helper()
	drop := map[string]bool{
		"BEADS_DIR": true, "BEADS_DB": true, "BD_DB": true,
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_COMMON_DIR": true,
	}
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !drop[key] {
			env = append(env, entry)
		}
	}
	return append(env, "HOME="+t.TempDir(), "BEADS_DISABLE_AUTO_UPDATE=1")
}
