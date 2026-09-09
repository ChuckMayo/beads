package agents

import (
	"strings"
	"testing"
)

func TestGeneratedAgentSectionsNeverAssertGitAuthority(t *testing.T) {
	surfaces := map[string]string{
		"full":    RenderSection(ProfileFull),
		"minimal": RenderSection(ProfileMinimal),
		"codex":   CodexSectionBody(),
	}
	for name, content := range surfaces {
		t.Run(name, func(t *testing.T) {
			lower := strings.ToLower(content)
			for _, forbidden := range []string{
				"git authority",
				"do not commit",
				"do not push",
				"commit only",
				"push only",
				"unless explicitly authorized",
			} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("generated %s agent section asserted source-control authority with %q:\n%s", name, forbidden, content)
				}
			}
		})
	}
}
