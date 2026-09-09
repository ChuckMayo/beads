package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestResolveChangeDirBeadsDirDoesNotChangeCWD(t *testing.T) {
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})

	startDir := t.TempDir()
	t.Chdir(startDir)

	projectDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(projectDir); err == nil {
		projectDir = resolved
	}
	beadsDir := filepath.Join(projectDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := resolveChangeDirBeadsDir(projectDir)
	if err != nil {
		t.Fatalf("resolveChangeDirBeadsDir: %v", err)
	}
	if got != beadsDir {
		t.Fatalf("resolveChangeDirBeadsDir() = %q, want %q", got, beadsDir)
	}

	afterWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd after resolve: %v", err)
	}
	if afterWD != startDir {
		t.Fatalf("working directory changed to %q, want %q", afterWD, startDir)
	}
}

func TestResolveChangeDirBeadsDirRejectsFile(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := resolveChangeDirBeadsDir(filePath); err == nil {
		t.Fatal("expected non-directory -C target to fail")
	}
}

func TestResolveChangeDirBeadsDirRejectsDirectoryWithoutProject(t *testing.T) {
	// Keep this fixture outside any developer-owned ancestor workspace. A
	// task-local TMPDIR may itself live below a valid ~/.beads directory.
	dir, err := os.MkdirTemp("/tmp", "beads-no-project-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if _, err := resolveChangeDirBeadsDir(dir); err == nil {
		t.Fatal("expected -C target without a beads project to fail")
	}
}

func TestApplyChangeDirSelectionRestoresProcessContext(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	originalChangeDir := changeDir
	t.Cleanup(func() {
		restoreChangeDirSelection()
		changeDir = originalChangeDir
		_ = os.Chdir(originalDir)
	})

	target := t.TempDir()
	beadsDir := filepath.Join(target, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEADS_DIR", "/original/beads")
	changeDir = target
	if err := applyChangeDirSelection(); err != nil {
		t.Fatalf("applyChangeDirSelection: %v", err)
	}
	wantTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Getwd(); got != wantTarget {
		t.Fatalf("cwd after apply = %q, want %q", got, wantTarget)
	}
	wantBeadsDir, err := filepath.EvalSymlinks(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("BEADS_DIR"); got != wantBeadsDir {
		t.Fatalf("BEADS_DIR after apply = %q, want %q", got, wantBeadsDir)
	}

	restoreChangeDirSelection()
	if got, _ := os.Getwd(); got != originalDir {
		t.Fatalf("cwd after restore = %q, want %q", got, originalDir)
	}
	if got := os.Getenv("BEADS_DIR"); got != "/original/beads" {
		t.Fatalf("BEADS_DIR after restore = %q, want original value", got)
	}
}

func TestApplyChangeDirSelectionFailureLeavesProcessContextUntouched(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	originalChangeDir := changeDir
	t.Cleanup(func() {
		restoreChangeDirSelection()
		changeDir = originalChangeDir
		_ = os.Chdir(originalDir)
	})

	t.Setenv("BEADS_DIR", "/original/beads")
	changeDir = filepath.Join(t.TempDir(), "missing")
	if err := applyChangeDirSelection(); err == nil {
		t.Fatal("invalid -C unexpectedly succeeded")
	}
	if got, _ := os.Getwd(); got != originalDir {
		t.Fatalf("failed apply changed cwd to %q, want %q", got, originalDir)
	}
	if got := os.Getenv("BEADS_DIR"); got != "/original/beads" {
		t.Fatalf("failed apply changed BEADS_DIR to %q", got)
	}
}

func TestIsPreviewCommand(t *testing.T) {
	tests := []struct {
		name string
		flag string
		set  string
		want bool
	}{
		{name: "dry run", flag: "dry-run", set: "true", want: true},
		{name: "inspect", flag: "inspect", set: "true", want: true},
		{name: "false preview flag", flag: "dry-run", set: "false", want: false},
		{name: "no preview flag", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			if tt.flag != "" {
				cmd.Flags().Bool(tt.flag, false, "")
				if err := cmd.Flags().Set(tt.flag, tt.set); err != nil {
					t.Fatalf("set %s: %v", tt.flag, err)
				}
			}
			if got := isPreviewCommand(cmd); got != tt.want {
				t.Fatalf("isPreviewCommand() = %v, want %v", got, tt.want)
			}
		})
	}
}
