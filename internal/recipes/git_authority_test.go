package recipes

import (
	"strings"
	"testing"
)

func TestGeneratedRecipesNeverAssertGitAuthority(t *testing.T) {
	for name, content := range map[string]string{
		"default": Template,
		"copilot": CopilotInstructionsTemplate,
	} {
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
					t.Fatalf("generated %s recipe asserted source-control authority with %q:\n%s", name, forbidden, content)
				}
			}
		})
	}
}
