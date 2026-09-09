package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/metrics"
)

var (
	primeFullMode     bool
	primeMCPMode      bool
	primeStealthMode  bool
	primeExportMode   bool
	primeMemoriesOnly bool
	primeHookJSONMode bool
)

const (
	primeStoreTimeoutEnv     = "BEADS_PRIME_TIMEOUT"
	primeStoreTimeoutDefault = 10 * time.Second
)

var ensureStoreActiveForPrime = ensureStoreActiveWithContext

func primeStoreTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(primeStoreTimeoutEnv))
	if raw == "" {
		return primeStoreTimeoutDefault
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d > 0 {
			return d
		}
		return primeStoreTimeoutDefault
	}
	if d, err := time.ParseDuration(raw + "s"); err == nil {
		if d > 0 {
			return d
		}
		return primeStoreTimeoutDefault
	}
	return primeStoreTimeoutDefault
}

// resolveGlobalPrimePath returns the path to ~/.config/beads/PRIME.md if it
// exists. configDirOverride is used for testing; pass "" for production.
func resolveGlobalPrimePath(configDirOverride string) string {
	var configDir string
	if configDirOverride != "" {
		configDir = configDirOverride
	} else {
		var err error
		configDir, err = os.UserConfigDir()
		if err != nil {
			return ""
		}
	}
	p := filepath.Join(configDir, "beads", "PRIME.md")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

var primeCmd = &cobra.Command{
	Use:     "prime",
	GroupID: "setup",
	Short:   "Output AI-optimized workflow context",
	Long: `Output essential Beads workflow context in AI-optimized markdown format.

Automatically detects if MCP server is active and adapts output:
- MCP mode: Brief workflow reminders (~50 tokens)
- CLI mode: Full command reference (~1-2k tokens)

Designed for Claude Code, Gemini CLI, and Codex SessionStart hooks to prevent
agents from forgetting bd workflow after context compaction.

Config options:
- no-git-ops: Legacy name for hiding Beads-managed Git integration hints.
  This setting never grants or revokes source-control permissions.
  Set via: bd config set no-git-ops true

	Workflow customization:
	- Place a .beads/PRIME.md file in the local clone or resolved workspace to override the default output entirely.
	- Use --export to dump the default content for customization.
	- Use --memories-only for hook contexts that should inject only persistent memories.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("prime")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		emit := func(content string) {
			if primeHookJSONMode {
				_ = outputHookJSON(os.Stdout, content)
			} else {
				fmt.Print(content)
			}
		}

		beadsDir := beads.FindBeadsDir()
		if beadsDir == "" {
			// Silent exit with success enables cross-platform hook integration.
			// Under --hook-json still emit a valid empty envelope.
			if primeHookJSONMode {
				_ = outputHookJSON(os.Stdout, "")
			}
			return nil
		}

		// Detect MCP mode (unless overridden by flags)
		mcpMode := isMCPActive()
		if primeFullMode {
			mcpMode = false
		}
		if primeMCPMode {
			mcpMode = true
		}

		stealthMode := primeStealthMode || config.GetBool("no-git-ops")

		if !primeExportMode {
			localPrimePath := filepath.Join(".beads", "PRIME.md")
			redirectedPrimePath := filepath.Join(beadsDir, "PRIME.md")

			// #nosec G304 -- path is relative to cwd
			if content, err := os.ReadFile(localPrimePath); err == nil {
				emit(string(content))
				return nil
			}
			// #nosec G304 -- path is constructed from beadsDir which we control
			if content, err := os.ReadFile(redirectedPrimePath); err == nil {
				emit(string(content))
				return nil
			}
			// #nosec G304 -- path constructed from UserConfigDir which we control
			if globalPath := resolveGlobalPrimePath(""); globalPath != "" {
				if content, err := os.ReadFile(globalPath); err == nil {
					emit(string(content))
					return nil
				}
			}
		}

		var buf bytes.Buffer
		if err := outputPrimeContextWithOptions(&buf, mcpMode, stealthMode, primeMemoriesOnly); err != nil {
			// Errors are suppressed by design for hook integration.
			if primeHookJSONMode {
				_ = outputHookJSON(os.Stdout, "")
			}
			return nil
		}
		// Append the AGENTS.md/CLAUDE.md divergence reminder only when both
		// files are independent regulars carrying the bd marker; otherwise this
		// adds nothing (zero output, negligible cost).
		buf.WriteString(primeDivergenceReminder(""))
		emit(buf.String())
		return nil
	},
}

func init() {
	primeCmd.Flags().BoolVar(&primeFullMode, "full", false, "Force full CLI output (ignore MCP detection)")
	primeCmd.Flags().BoolVar(&primeMCPMode, "mcp", false, "Force MCP mode (minimal output)")
	primeCmd.Flags().BoolVar(&primeStealthMode, "stealth", false, "Hide Beads-managed Git integration hints")
	primeCmd.Flags().BoolVar(&primeExportMode, "export", false, "Output default content (ignores PRIME.md override)")
	primeCmd.Flags().BoolVar(&primeMemoriesOnly, "memories-only", false, "Output only persistent memories for compact hook contexts")
	primeCmd.Flags().BoolVar(&primeHookJSONMode, "hook-json", false, "Wrap output in the SessionStart hook JSON envelope (Claude Code, Gemini CLI, Codex)")
	rootCmd.AddCommand(primeCmd)
}

// outputHookJSON wraps content in the SessionStart hook JSON envelope shared
// by Claude Code, Gemini CLI, and Codex. All three require stdout to be valid
// JSON — no plain text may be emitted alongside it. See:
// https://geminicli.com/docs/hooks/reference/
func outputHookJSON(w io.Writer, content string) error {
	type hookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	envelope := struct {
		HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
	}{
		HookSpecificOutput: hookSpecificOutput{
			HookEventName:     "SessionStart",
			AdditionalContext: content,
		},
	}
	return json.NewEncoder(w).Encode(envelope)
}

// isMCPActive detects if MCP server is currently active
func isMCPActive() bool {
	// Get home directory with fallback
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback to HOME environment variable
		home = os.Getenv("HOME")
		if home == "" {
			// Can't determine home directory, assume no MCP
			return false
		}
	}

	settingsPath := filepath.Join(home, ".claude/settings.json")
	// #nosec G304 -- settings path derived from user home directory
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return false
	}

	// Check mcpServers section for beads
	mcpServers, ok := settings["mcpServers"].(map[string]interface{})
	if !ok {
		return false
	}

	// Look for beads server (any key containing "beads")
	for key := range mcpServers {
		if strings.Contains(strings.ToLower(key), "beads") {
			return true
		}
	}

	return false
}

// isEphemeralBranch detects if current branch has no upstream (ephemeral/local-only).
// Uses process CWD git independently of BEADS_DIR validation (GH#4927).
var isEphemeralBranch = func() bool {
	// git rev-parse --abbrev-ref --symbolic-full-name @{u}
	// Returns error code 128 if no upstream configured
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	return cmd.Run() != nil
}

// primeNoPushConfigured reports whether the "no-push" config flag is set
// (stubbable for tests).
var primeNoPushConfigured = func() bool {
	return config.GetBool("no-push")
}

// primeHasGitRemote detects if any git remote is configured (stubbable for tests).
//
// GH#4927: Probe the process CWD git workspace directly. Do not require a valid
// RepoContext / BEADS_DIR — external BEADS_DIR under another account's home
// (common in agent sandboxes) fails isPathInSafeBoundary and must not be
// misreported as "no git remote" when `git remote` in CWD succeeds. SEC-003
// boundary on BEADS_DIR remains enforced elsewhere.
var primeHasGitRemote = func() bool {
	return gitCWDHasRemote()
}

// gitCWDHasRemote reports whether the process CWD git repo has any remote.
// Delegates to gitDirHasRemote so the production path and the test-driven
// path share one implementation (no BEADS_DIR coupling).
func gitCWDHasRemote() bool {
	return gitDirHasRemote("")
}

// gitDirHasRemote reports whether the git repo at dir has any remote
// configured. dir == "" runs git in the process's current working directory
// (this is what gitCWDHasRemote uses); a non-empty dir lets tests probe an
// explicit fixture repo without chdir-ing the whole process.
func gitDirHasRemote(dir string) bool {
	cmd := exec.Command("git", "remote")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// getRedirectNotice returns a notice string if beads is redirected
func getRedirectNotice(verbose bool) string {
	redirectInfo := beads.GetRedirectInfo()
	if !redirectInfo.IsRedirected {
		return ""
	}

	if verbose {
		return fmt.Sprintf(`> ⚠️ **Redirected**: Local .beads → %s
> You share issues with other clones using this redirect.

`, redirectInfo.TargetDir)
	}
	return fmt.Sprintf("**Note**: Beads redirected to %s (shared with other clones)\n\n", redirectInfo.TargetDir)
}

// outputPrimeContext outputs workflow context in markdown format
func outputPrimeContext(w io.Writer, mcpMode bool, stealthMode bool) error {
	return outputPrimeContextWithOptions(w, mcpMode, stealthMode, false)
}

func outputPrimeContextWithOptions(w io.Writer, mcpMode bool, stealthMode bool, memoriesOnly bool) error {
	if memoriesOnly {
		return outputMemoriesOnlyContext(w)
	}
	if mcpMode {
		return outputMCPContext(w, stealthMode)
	}
	return outputCLIContext(w, stealthMode)
}

const primeTruncationDirective = "[bd prime] If this output is truncated by your host, read the full persisted hook output before continuing; it may contain project memories and session rules not visible in the preview.\n\n"

func outputMemoriesOnlyContext(w io.Writer) error {
	_, _ = fmt.Fprint(w, primeTruncationDirective)
	if mem := formatMemoriesForPrime(false); mem != "" {
		_, _ = fmt.Fprint(w, mem)
		return nil
	}
	_, _ = fmt.Fprint(w, "# Beads Persistent Memories\n\nNo memories stored. Use `bd remember \"insight\"` to add one.\n")
	return nil
}

// formatMemoriesForPrime queries memories from the k/v store and formats them for injection.
// Returns empty string if no memories or if store is unavailable.
func formatMemoriesForPrime(compact bool) string {
	// Try to initialize store if not already active (prime may run before other commands)
	if store == nil {
		timeout := primeStoreTimeout()
		ctx := context.Background()
		var cancel context.CancelFunc
		if timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		if err := ensureStoreActiveForPrime(ctx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return formatPrimeMemoryTimeout(compact, timeout)
			}
			return "" // Silently skip — store unavailable
		}
	}
	if store == nil {
		return ""
	}
	ctx := context.Background()
	allConfig, err := store.GetAllConfig(ctx)
	if err != nil {
		return ""
	}

	fullPrefix := kvPrefix + memoryPrefix
	var keys []string
	memories := make(map[string]string)
	for k, v := range allConfig {
		if strings.HasPrefix(k, fullPrefix) {
			userKey := strings.TrimPrefix(k, fullPrefix)
			memories[userKey] = v
			keys = append(keys, userKey)
		}
	}
	if len(memories) == 0 {
		return ""
	}
	sort.Strings(keys)

	var sb strings.Builder
	if compact {
		sb.WriteString("\n## Memories\n")
		for _, k := range keys {
			// Compact: one line per memory
			v := strings.ReplaceAll(memories[k], "\n", " ")
			if len(v) > 150 {
				v = v[:147] + "..."
			}
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", k, v))
		}
	} else {
		sb.WriteString(fmt.Sprintf("\n## Persistent Memories (%d)\n\n", len(memories)))
		sb.WriteString("Stored via `bd remember`. Update in place with `bd remember --key <key> \"new content\"`. Search with `bd memories <keyword>`. Remove with `bd forget <key>`.\n\n")
		for _, k := range keys {
			sb.WriteString(fmt.Sprintf("### %s\n%s\n\n", k, memories[k]))
		}
	}
	return sb.String()
}

func formatPrimeMemoryTimeout(compact bool, timeout time.Duration) string {
	if timeout <= 0 {
		timeout = primeStoreTimeoutDefault
	}
	msg := fmt.Sprintf("Skipped: timed out after %s opening beads storage. Another bd process or stale storage lock may be blocking memory injection; run `bd doctor` and stop stuck bd processes before retrying.", timeout.Round(time.Millisecond))
	if compact {
		return "\n## Memories\n- " + msg + "\n"
	}
	return "\n## Persistent Memories\n\n" + msg + "\n"
}

// outputMCPContext outputs minimal context for MCP users
func outputMCPContext(w io.Writer, stealthMode bool) error {
	_ = stealthMode // Retained for CLI compatibility; it never controls source Git.

	redirectNotice := getRedirectNotice(false)
	memories := formatMemoriesForPrime(true)

	context := primeTruncationDirective + `# Beads Issue Tracker Active

` + redirectNotice
	if memories != "" {
		context += memories + "\n"
	}

	context += `# 🚨 SESSION CLOSE PROTOCOL 🚨

Before saying "done": bd close <completed-ids>; run relevant checks; report issue state and validation.

## Core Rules
- **Default**: Use beads for ALL task tracking (` + "`bd create`" + `, ` + "`bd ready`" + `, ` + "`bd close`" + `)
- **Prohibited**: Do NOT use TodoWrite, TaskCreate, or markdown files for task tracking
- **Workflow**: Create beads issue BEFORE writing code, mark in_progress when starting
- **Memory**: Use ` + "`bd remember`" + ` for persistent knowledge. Do NOT use MEMORY.md files.
- Persistence you don't need beats lost context
- **Source control**: Follow the current user, orchestrator, and repository instructions. Beads does not grant or revoke source-control permissions.

Start: Check ` + "`ready`" + ` tool for available work.
`
	_, _ = fmt.Fprint(w, context)

	return nil
}

// outputCLIContext outputs full CLI reference for non-MCP users
func outputCLIContext(w io.Writer, stealthMode bool) error {
	_ = stealthMode // Retained for CLI compatibility; it never controls source Git.
	closeProtocol := `[ ] 1. bd close <id1> <id2> ...   (close completed issues)
[ ] 2. run quality gates        (tests, linters, builds when relevant)
[ ] 3. sync Beads if appropriate (bd dolt push)
[ ] 4. report issue state and validation`
	closeNote := "**Source control:** Follow the current user, orchestrator, and repository instructions. Beads does not grant or revoke source-control permissions."
	syncSection := `### Sync & Collaboration
- ` + "`bd dolt push`" + ` - Push Beads issue data to its Dolt remote
- ` + "`bd dolt pull`" + ` - Pull Beads issue data from its Dolt remote
- ` + "`bd search <query>`" + ` - Search issues by keyword`
	completingWorkflow := `**Completing work:**
` + "```bash" + `
bd close <id1> <id2> ...    # Close all completed issues at once
bd dolt push                # Sync Beads issue data when configured
` + "```"

	redirectNotice := getRedirectNotice(true)
	memories := formatMemoriesForPrime(false)

	context := primeTruncationDirective + `# Beads Workflow Context

> **Context Recovery**: Run ` + "`bd prime`" + ` after compaction, clear, or new session
> Hooks auto-call this in Claude Code and Codex when a beads workspace is resolved

` + redirectNotice
	if memories != "" {
		context += memories + "\n"
	}

	context += `# 🚨 SESSION CLOSE PROTOCOL 🚨

**CRITICAL**: Before saying "done" or "complete", you MUST run this checklist:

` + "```" + `
` + closeProtocol + `
` + "```" + `

` + closeNote + `

## Core Rules
- **Default**: Use beads for ALL task tracking (` + "`bd create`" + `, ` + "`bd ready`" + `, ` + "`bd close`" + `)
- **Prohibited**: Do NOT use TodoWrite, TaskCreate, or markdown files for task tracking
- **Workflow**: Create beads issue BEFORE writing code, mark in_progress when starting
- **Memory**: Use ` + "`bd remember \"insight\"`" + ` for persistent knowledge across sessions. Do NOT use MEMORY.md files — they fragment across accounts. Search with ` + "`bd memories <keyword>`" + `.
- Persistence you don't need beats lost context
- **Source control**: Follow the current user, orchestrator, and repository instructions. Beads does not grant or revoke source-control permissions.
- Session management: check ` + "`bd ready`" + ` for available work

## Essential Commands

### Finding Work
- ` + "`bd ready`" + ` - Show issues ready to work (no blockers)
- ` + "`bd list --status=open`" + ` - All open issues
- ` + "`bd list --status=in_progress`" + ` - Your active work
- ` + "`bd show <id>`" + ` - Detailed issue view with dependencies

### Creating & Updating
- ` + "`bd create --title=\"Summary of this issue\" --description=\"Why this issue exists and what needs to be done\" --type=task|bug|feature --priority=2`" + ` - New issue
  - Priority: 0-4 or P0-P4 (0=critical, 2=medium, 4=backlog). NOT "high"/"medium"/"low"
- ` + "`bd create ... --parent=<id>`" + ` - Hierarchical child (task under epic, subtask under task; inherits parent labels)
- ` + "`bd update <id> --claim`" + ` - Claim work
- ` + "`bd update <id> --assignee=username`" + ` - Assign to someone
- ` + "`bd update <id> --title/--description/--notes/--design`" + ` - Update fields inline
- ` + "`bd close <id>`" + ` - Mark complete
- ` + "`bd close <id1> <id2> ...`" + ` - Close multiple issues at once (more efficient)
- ` + "`bd close <id> --reason=\"explanation\"`" + ` - Close with reason
- **Tip**: When creating multiple issues/tasks/epics, use parallel subagents for efficiency
- **WARNING**: Do NOT use ` + "`bd edit`" + ` - it opens $EDITOR (vim/nano) which blocks agents

### Dependencies & Blocking
- ` + "`bd dep add <issue> <depends-on>`" + ` - Add dependency (issue depends on depends-on)
- ` + "`bd blocked`" + ` - Show all blocked issues
- ` + "`bd show <id>`" + ` - See what's blocking/blocked by this issue

` + syncSection + `

### Project Health
- ` + "`bd stats`" + ` - Project statistics (open/closed/blocked counts)
- ` + "`bd doctor`" + ` - Check for issues (sync problems, missing hooks)
- ` + "`bd doctor --check=conventions`" + ` - Check for convention drift (lint, stale, orphans)

### Quality Tools
- ` + "`bd create --validate`" + ` - Check description has required sections
- ` + "`bd create --acceptance=\"criteria\"`" + ` - Set acceptance criteria (checked by --validate)
- ` + "`bd create --design=\"decisions\"`" + ` - Record design decisions
- ` + "`bd create --notes=\"context\"`" + ` - Add supplementary notes
- ` + "`bd config set validation.on-create warn`" + ` - Auto-validate on every create
- ` + "`bd lint`" + ` - Check existing issues for missing sections

### Lifecycle & Hygiene
- ` + "`bd defer <id> --until=\"date\"`" + ` - Defer work to a future date
- ` + "`bd supersede <id> --with=<new-id>`" + ` - Mark issue as superseded
- ` + "`bd close <id> --suggest-next`" + ` - Show newly unblocked issues after closing
- ` + "`bd stale`" + ` - Find issues with no recent activity
- ` + "`bd orphans`" + ` - Find issues with broken dependencies
- ` + "`bd preflight`" + ` - Pre-PR checks (lint, stale, orphans)
- ` + "`bd human <id>`" + ` - Flag for human decision (list/respond/dismiss)

### Structured Workflows
- ` + "`bd formula list`" + ` - See available workflow templates
- ` + "`bd mol pour <name>`" + ` - Start structured workflow from formula

## Common Workflows

**Starting work:**
` + "```bash" + `
bd ready           # Find available work
bd show <id>       # Review issue details
bd update <id> --claim  # Claim it
` + "```" + `

` + completingWorkflow + `

**Creating dependent work:**
` + "```bash" + `
# Run bd create commands in parallel (use subagents for many items)
bd create --title="Implement feature X" --description="Why this issue exists and what needs to be done" --type=feature
bd create --title="Write tests for X" --description="Why this issue exists and what needs to be done" --type=task
bd dep add beads-yyy beads-xxx  # Tests depend on Feature (Feature blocks tests)
` + "```" + `
`
	_, _ = fmt.Fprint(w, context)

	return nil
}
