package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubPrimeStoreUnavailable temporarily disconnects prime from any ambient
// workspace store, so tests assert against prime's own text rather than
// whatever database (and persistent memories) happens to exist in an ancestor
// directory of the test process. Without this, a developer or verify runner
// whose checkout is nested under a real beads workspace gets that workspace's
// live memory text injected into the output under test — e.g. a memory
// containing "git pull" fails the stealth-mode rejectText assertions. CI never
// sees the leak because its checkouts have no ancestor database.
//
// Returns a function to restore the original store wiring.
// Usage:
//
//	defer stubPrimeStoreUnavailable()()
func stubPrimeStoreUnavailable() func() {
	origStore := store
	origActive := storeActive
	origEnsure := ensureStoreActiveForPrime
	store = nil
	storeActive = false
	ensureStoreActiveForPrime = func(context.Context) error {
		return errors.New("prime store stubbed out in test")
	}
	return func() {
		store = origStore
		storeActive = origActive
		ensureStoreActiveForPrime = origEnsure
	}
}

func TestOutputContextFunction(t *testing.T) {
	defer stubPrimeStoreUnavailable()()
	for _, mcpMode := range []bool{false, true} {
		for _, stealthMode := range []bool{false, true} {
			for _, ephemeralMode := range []bool{false, true} {
				for _, hasRemote := range []bool{false, true} {
					for _, noPush := range []bool{false, true} {
						defer stubIsEphemeralBranch(ephemeralMode)()
						defer stubPrimeHasGitRemote(hasRemote)()
						defer stubPrimeNoPushConfigured(noPush)()

						var buf bytes.Buffer
						if err := outputPrimeContext(&buf, mcpMode, stealthMode); err != nil {
							t.Fatalf("outputPrimeContext failed: %v", err)
						}
						output := buf.String()
						wantHeading := "Beads Workflow Context"
						if mcpMode {
							wantHeading = "Beads Issue Tracker Active"
						}
						for _, expected := range []string{wantHeading, "bd close", "Beads does not grant or revoke"} {
							if !strings.Contains(output, expected) {
								t.Errorf("expected text not found: %s", expected)
							}
						}
						assertNoGeneratedGitAuthority(t, output)
					}
				}
			}
		}
	}
}

func TestPrimeClaimGuidanceUsesAtomicClaim(t *testing.T) {
	defer stubPrimeStoreUnavailable()()
	defer stubIsEphemeralBranch(false)()
	defer stubPrimeHasGitRemote(true)()

	var buf bytes.Buffer
	if err := outputPrimeContext(&buf, false, false); err != nil {
		t.Fatalf("outputPrimeContext failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "bd update <id> --claim") {
		t.Fatal("prime output should teach bd update <id> --claim")
	}
	if strings.Contains(output, "bd update <id> --status=in_progress") {
		t.Fatal("prime output should not teach bd update <id> --status=in_progress")
	}
}

func TestPrimeStartsWithTruncationDirective(t *testing.T) {
	defer stubPrimeStoreUnavailable()()
	defer stubIsEphemeralBranch(false)()
	defer stubPrimeHasGitRemote(true)()

	for _, mcpMode := range []bool{false, true} {
		var buf bytes.Buffer
		if err := outputPrimeContext(&buf, mcpMode, false); err != nil {
			t.Fatalf("outputPrimeContext failed: %v", err)
		}
		if !strings.HasPrefix(buf.String(), primeTruncationDirective) {
			t.Fatalf("prime output should start with truncation directive; got %q", buf.String()[:min(120, buf.Len())])
		}
	}
}

func TestPrimeMemoriesOnlyNoMemories(t *testing.T) {
	defer stubPrimeStoreUnavailable()()
	var buf bytes.Buffer
	if err := outputPrimeContextWithOptions(&buf, false, false, true); err != nil {
		t.Fatalf("outputPrimeContextWithOptions failed: %v", err)
	}

	output := buf.String()
	if !strings.HasPrefix(output, primeTruncationDirective) {
		t.Fatal("memories-only output should start with truncation directive")
	}
	if strings.Contains(output, "Essential Commands") {
		t.Fatalf("memories-only output should not include the full workflow guide: %s", output)
	}
}

func TestFormatMemoriesForPrimeTimesOutOpeningStore(t *testing.T) {
	oldStore := store
	oldStoreActive := storeActive
	oldEnsure := ensureStoreActiveForPrime
	store = nil
	storeActive = false
	ensureStoreActiveForPrime = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() {
		store = oldStore
		storeActive = oldStoreActive
		ensureStoreActiveForPrime = oldEnsure
	})
	t.Setenv(primeStoreTimeoutEnv, "1ms")

	out := formatMemoriesForPrime(false)
	if !strings.Contains(out, "timed out") {
		t.Fatalf("expected timeout warning in prime memory output, got %q", out)
	}
	if !strings.Contains(out, "stale storage lock") {
		t.Fatalf("expected stale-lock guidance in prime memory output, got %q", out)
	}
}

func TestPrimeStoreTimeoutNonPositiveUsesDefault(t *testing.T) {
	for _, value := range []string{"0", "0s", "-5s"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(primeStoreTimeoutEnv, value)
			if got := primeStoreTimeout(); got != primeStoreTimeoutDefault {
				t.Fatalf("primeStoreTimeout() = %s, want default %s", got, primeStoreTimeoutDefault)
			}
		})
	}
}

func TestPrimeContextUsesWorkspaceLanguage(t *testing.T) {
	defer stubPrimeStoreUnavailable()()
	defer stubIsEphemeralBranch(false)()
	defer stubPrimeHasGitRemote(true)()

	var buf bytes.Buffer
	if err := outputPrimeContext(&buf, false, false); err != nil {
		t.Fatalf("outputPrimeContext failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "resolved") || !strings.Contains(output, "workspace") {
		t.Fatalf("prime output should describe resolved workspace semantics: %s", output)
	}
	if strings.Contains(output, "when .beads/ detected") {
		t.Fatal("prime output should not imply local .beads detection is required")
	}
}

// stubIsEphemeralBranch temporarily replaces isEphemeralBranch
// with a stub returning returnValue.
//
// Returns a function to restore the original isEphemeralBranch.
// Usage:
//
//	defer stubIsEphemeralBranch(true)()
func stubIsEphemeralBranch(isEphem bool) func() {
	original := isEphemeralBranch
	isEphemeralBranch = func() bool {
		return isEphem
	}
	return func() {
		isEphemeralBranch = original
	}
}

// stubPrimeHasGitRemote temporarily replaces primeHasGitRemote
// with a stub returning returnValue.
//
// Returns a function to restore the original primeHasGitRemote.
// Usage:
//
//	defer stubPrimeHasGitRemote(true)()
func stubPrimeHasGitRemote(hasRemote bool) func() {
	original := primeHasGitRemote
	primeHasGitRemote = func() bool {
		return hasRemote
	}
	return func() {
		primeHasGitRemote = original
	}
}

// stubPrimeNoPushConfigured temporarily replaces primeNoPushConfigured
// with a stub returning noPush.
func stubPrimeNoPushConfigured(noPush bool) func() {
	original := primeNoPushConfigured
	primeNoPushConfigured = func() bool {
		return noPush
	}
	return func() {
		primeNoPushConfigured = original
	}
}

func TestPrimeGlobalFallback(t *testing.T) {
	// Create a temp directory to act as config dir
	tmpDir := t.TempDir()
	beadsConfigDir := filepath.Join(tmpDir, "beads")
	if err := os.MkdirAll(beadsConfigDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	content := "# Global PRIME override\nCustom instructions here.\n"
	if err := os.WriteFile(filepath.Join(beadsConfigDir, "PRIME.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write PRIME.md: %v", err)
	}

	// Call the helper that resolves the global prime path
	got := resolveGlobalPrimePath(tmpDir)
	if got == "" {
		t.Fatal("resolveGlobalPrimePath returned empty, want path to global PRIME.md")
	}

	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", got, err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", string(data), content)
	}
}

func TestPrimeGlobalFallback_Missing(t *testing.T) {
	// When no global PRIME.md exists, should return empty string
	tmpDir := t.TempDir()
	got := resolveGlobalPrimePath(tmpDir)
	if got != "" {
		t.Errorf("resolveGlobalPrimePath = %q, want empty for missing file", got)
	}
}

// hookJSONEnvelope mirrors the JSON shape produced by outputHookJSON —
// kept in test code so the assertion fails loudly if the production shape
// drifts.
type hookJSONEnvelope struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func TestOutputHookJSON_ShapeWithContent(t *testing.T) {
	var buf bytes.Buffer
	const payload = "# Hello\n\nbd ready\n"
	if err := outputHookJSON(&buf, payload); err != nil {
		t.Fatalf("outputHookJSON: %v", err)
	}

	// json.Encoder.Encode appends a trailing newline; the JSON itself must
	// still be valid.
	out := strings.TrimRight(buf.String(), "\n")

	var env hookJSONEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if env.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q, want SessionStart", env.HookSpecificOutput.HookEventName)
	}
	if env.HookSpecificOutput.AdditionalContext != payload {
		t.Errorf("additionalContext = %q, want %q", env.HookSpecificOutput.AdditionalContext, payload)
	}
}

func TestOutputHookJSON_EmptyContent(t *testing.T) {
	// Empty envelope is the contract for "nothing to inject" — the hook host
	// still requires valid JSON on stdout, so we cannot just emit nothing.
	var buf bytes.Buffer
	if err := outputHookJSON(&buf, ""); err != nil {
		t.Fatalf("outputHookJSON: %v", err)
	}

	var env hookJSONEnvelope
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if env.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q, want SessionStart", env.HookSpecificOutput.HookEventName)
	}
	if env.HookSpecificOutput.AdditionalContext != "" {
		t.Errorf("additionalContext = %q, want empty", env.HookSpecificOutput.AdditionalContext)
	}
}

// TestPrime_RawMarkdown_NotJSON_WithoutFlag is a regression guard: without
// --hook-json, prime output must remain raw markdown (used by CLI users and
// any hook-free integrations). It would be a regression if the JSON envelope
// leaked into the default path.
func TestPrime_RawMarkdown_NotJSON_WithoutFlag(t *testing.T) {
	defer stubPrimeStoreUnavailable()()
	defer stubIsEphemeralBranch(false)()
	defer stubPrimeHasGitRemote(true)()

	var buf bytes.Buffer
	if err := outputPrimeContext(&buf, false, false); err != nil {
		t.Fatalf("outputPrimeContext: %v", err)
	}

	output := buf.String()
	if strings.HasPrefix(strings.TrimSpace(output), "{") {
		preview := output
		if len(preview) > 200 {
			preview = preview[:200]
		}
		t.Fatalf("prime output without --hook-json should be raw markdown, got JSON-looking content: %q", preview)
	}
	// Best-effort: confirm the raw markdown contract holds.
	var envelope map[string]interface{}
	if err := json.Unmarshal([]byte(output), &envelope); err == nil {
		t.Fatal("prime output without --hook-json should not be valid JSON (regression guard)")
	}
}
