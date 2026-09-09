package main

import (
	"bytes"
	"strings"
	"testing"
)

// Beads is an issue tracker, not a source-control authority. Generated agent
// context must therefore stay invariant across Git repository facts and Beads
// policy knobs: those inputs may change Beads' own behavior, but they must
// never manufacture an instruction that revokes commit or push authority.
func TestPrimeNeverEmitsGitAuthority(t *testing.T) {
	defer stubPrimeStoreUnavailable()()

	for _, mcpMode := range []bool{false, true} {
		for _, stealthMode := range []bool{false, true} {
			for _, ephemeralMode := range []bool{false, true} {
				for _, hasGitRemote := range []bool{false, true} {
					for _, noPush := range []bool{false, true} {
						name := strings.Join([]string{
							"mcp=" + boolName(mcpMode),
							"stealth=" + boolName(stealthMode),
							"ephemeral=" + boolName(ephemeralMode),
							"remote=" + boolName(hasGitRemote),
							"no-push=" + boolName(noPush),
						}, "/")
						t.Run(name, func(t *testing.T) {
							defer stubIsEphemeralBranch(ephemeralMode)()
							defer stubPrimeHasGitRemote(hasGitRemote)()
							defer stubPrimeNoPushConfigured(noPush)()

							var out bytes.Buffer
							if err := outputPrimeContext(&out, mcpMode, stealthMode); err != nil {
								t.Fatalf("outputPrimeContext: %v", err)
							}
							assertNoGeneratedGitAuthority(t, out.String())
						})
					}
				}
			}
		}
	}
}

func boolName(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func assertNoGeneratedGitAuthority(t *testing.T, output string) {
	t.Helper()
	lower := strings.ToLower(output)
	for _, forbidden := range []string{
		"git authority",
		"git workflow",
		"do not commit",
		"do not push",
		"commit only",
		"push only",
		"push disabled",
		"wait for authority",
		"explicit authority",
		"unless explicitly authorized",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("generated Beads context asserted source-control authority with %q:\n%s", forbidden, output)
		}
	}
}
