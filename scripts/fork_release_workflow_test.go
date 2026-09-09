package scripts_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestForkReleasePinsAndVerifiesMacOS11DeploymentTarget(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve test file path")
	}
	workflowPath := filepath.Join(filepath.Dir(currentFile), "..", ".github", "workflows", "fork-release.yml")
	contents, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read fork release workflow: %v", err)
	}
	workflow := string(contents)

	for _, required := range []string{
		`MACOSX_DEPLOYMENT_TARGET: "11.0"`,
		`vtool -show-build`,
		`unexpected minimum macOS version`,
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("fork release workflow does not enforce %q", required)
		}
	}
}
