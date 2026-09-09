package ownershiphandoff

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeHandoffToken = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestGCProviderCompletesOnlyThroughTrustedProtocol(t *testing.T) {
	city := canonicalTestDir(t)
	scope := filepath.Join(city, "scope")
	if err := os.Mkdir(scope, 0700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(city, "gc.log")
	errLogPath := filepath.Join(city, "gc.err")
	binary := fakeGCProtocol(t)
	t.Setenv("GC_HANDOFF_LOG", logPath)
	t.Setenv("GC_HANDOFF_ERR_LOG", errLogPath)
	request := Request{CityRoot: city, Root: scope, Database: "beads", Workspace: "ws",
		Endpoint: Endpoint{Host: "127.0.0.1", Port: 3307}, Owner: OwnerLegacyGC}
	provider, err := NewGCProvider(binary)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), request, filepath.Join(scope, "ownership-handoff.json"), provider, false)
	if err != nil || result.Phase != PhaseCommitted || result.Owner != OwnerBD || !result.Mutates {
		errLog, _ := os.ReadFile(errLogPath)
		t.Fatalf("result=%+v err=%v, want committed bd-owned handoff; fake stderr=%q", result, err, errLog)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(log)); got != "handoff-inspect\nhandoff-stop\nhandoff-inspect\nhandoff-inspect" {
		t.Fatalf("protocol operations=%q, want inspect, stop, then two post-stop checks", got)
	}
}

func TestGCProviderRefusalNeverInvokesStop(t *testing.T) {
	city := canonicalTestDir(t)
	scope := filepath.Join(city, "scope")
	if err := os.Mkdir(scope, 0700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(city, "gc.log")
	errLogPath := filepath.Join(city, "gc.err")
	binary := fakeGCProtocol(t)
	t.Setenv("GC_HANDOFF_LOG", logPath)
	t.Setenv("GC_HANDOFF_ERR_LOG", errLogPath)
	t.Setenv("GC_HANDOFF_REFUSE", "1")
	request := Request{CityRoot: city, Root: scope, Database: "beads", Workspace: "ws",
		Endpoint: Endpoint{Host: "127.0.0.1", Port: 3307}, Owner: OwnerLegacyGC}
	provider, err := NewGCProvider(binary)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), request, filepath.Join(scope, "ownership-handoff.json"), provider, false)
	if err == nil || result.ErrorCode != "process_unowned" || result.Mutates {
		errLog, _ := os.ReadFile(errLogPath)
		t.Fatalf("result=%+v err=%v, want process_unowned refusal; fake stderr=%q", result, err, errLog)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(log)); got != "handoff-inspect" {
		t.Fatalf("protocol operations=%q, want inspect only", got)
	}
}

func TestGCProviderRejectsUntrustedBinary(t *testing.T) {
	if _, err := NewGCProvider("gc"); err == nil {
		t.Fatal("relative GC binary accepted")
	}
}

func TestGCProviderCanonicalizesSymlinkedAncestor(t *testing.T) {
	physical := t.TempDir()
	link := filepath.Join(t.TempDir(), "gc-bin-dir")
	if err := os.Symlink(physical, link); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(physical, "gc")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	provider, err := NewGCProvider(filepath.Join(link, "gc"))
	if err != nil {
		t.Fatalf("NewGCProvider through a symlinked ancestor: %v", err)
	}
	if provider.(*GCProvider).Binary != binary {
		t.Fatalf("provider binary = %q, want canonical %q", provider.(*GCProvider).Binary, binary)
	}
}

func TestGCProviderRejectsIdentityAndTokenSubstitution(t *testing.T) {
	city := canonicalTestDir(t)
	scope := filepath.Join(city, "scope")
	if err := os.Mkdir(scope, 0700); err != nil {
		t.Fatal(err)
	}
	binary := fakeGCProtocol(t)
	t.Setenv("GC_HANDOFF_LOG", filepath.Join(city, "gc.log"))
	t.Setenv("GC_HANDOFF_ERR_LOG", filepath.Join(city, "gc.err"))
	request := Request{CityRoot: city, Root: scope, Database: "beads", Workspace: "ws", Endpoint: Endpoint{Host: "127.0.0.1", Port: 3307}, Owner: OwnerLegacyGC}
	provider, err := NewGCProvider(binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GC_HANDOFF_DATABASE", "other")
	if _, err := provider.OwnershipHandoffHooks(context.Background(), request); err == nil {
		t.Fatal("provider accepted substituted identity")
	}
	t.Setenv("GC_HANDOFF_DATABASE", "")
	t.Setenv("GC_HANDOFF_TOKEN", "not-a-token")
	if _, err := provider.OwnershipHandoffHooks(context.Background(), request); err == nil {
		t.Fatal("provider accepted malformed identity token")
	}
}

func TestGCProviderRefusesCommitWhenStopDidNotReleaseOwner(t *testing.T) {
	city := canonicalTestDir(t)
	scope := filepath.Join(city, "scope")
	if err := os.Mkdir(scope, 0700); err != nil {
		t.Fatal(err)
	}
	binary := fakeGCProtocol(t)
	t.Setenv("GC_HANDOFF_LOG", filepath.Join(city, "gc.log"))
	t.Setenv("GC_HANDOFF_ERR_LOG", filepath.Join(city, "gc.err"))
	t.Setenv("GC_HANDOFF_STOPPED_FILE", filepath.Join(city, "stopped"))
	t.Setenv("GC_HANDOFF_STOP_LEAVES_LIVE", "1")
	request := Request{CityRoot: city, Root: scope, Database: "beads", Workspace: "ws", Endpoint: Endpoint{Host: "127.0.0.1", Port: 3307}, Owner: OwnerLegacyGC}
	provider, err := NewGCProvider(binary)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), request, filepath.Join(scope, "ownership-handoff.json"), provider, false)
	if err == nil || result.Phase != PhaseOldOwnerStopped || result.Owner != OwnerLegacyGC || result.ErrorCode != "verification_failed" {
		t.Fatalf("result=%+v err=%v, want uncommitted verification failure", result, err)
	}
}

func TestGCProviderBoundsProtocolCommand(t *testing.T) {
	city := canonicalTestDir(t)
	scope := filepath.Join(city, "scope")
	if err := os.Mkdir(scope, 0700); err != nil {
		t.Fatal(err)
	}
	binary := fakeGCProtocol(t)
	t.Setenv("GC_HANDOFF_LOG", filepath.Join(city, "gc.log"))
	t.Setenv("GC_HANDOFF_ERR_LOG", filepath.Join(city, "gc.err"))
	t.Setenv("GC_HANDOFF_SLEEP", "1")
	provider, err := NewGCProvider(binary)
	if err != nil {
		t.Fatal(err)
	}
	provider.(*GCProvider).timeout = 10 * time.Millisecond
	request := Request{CityRoot: city, Root: scope, Database: "beads", Workspace: "ws", Endpoint: Endpoint{Host: "127.0.0.1", Port: 3307}, Owner: OwnerLegacyGC}
	if _, err := provider.OwnershipHandoffHooks(context.Background(), request); err == nil {
		t.Fatal("provider accepted a timed-out protocol command")
	}
}

func TestDecodeGCResponseRejectsUnknownFieldsAndTrailingObjects(t *testing.T) {
	response := gcHandoffResponse{SchemaVersion: gcHandoffSchemaVersion, Operation: "handoff-inspect",
		Result: "eligible", Owner: OwnerLegacyGC, IdentityToken: fakeHandoffToken}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeGCResponse(append(raw, []byte("{}")...)); err == nil {
		t.Fatal("trailing JSON object accepted")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["extra"] = json.RawMessage("true")
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeGCResponse(raw); err == nil {
		t.Fatal("unknown response field accepted")
	}
}

func canonicalTestDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func fakeGCProtocol(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gc")
	script := `#!/bin/sh
set -e
exec 2>"$GC_HANDOFF_ERR_LOG"
operation="$2"
city=""
scope=""
database=""
workspace=""
host=""
port=""
socket=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --city) city="$2"; shift 2 ;;
    --scope-root) scope="$2"; shift 2 ;;
    --database) database="$2"; shift 2 ;;
    --workspace) workspace="$2"; shift 2 ;;
    --host) host="$2"; shift 2 ;;
    --port) port="$2"; shift 2 ;;
    --socket) socket="$2"; shift 2 ;;
    --identity-token) shift 2 ;;
    *) shift ;;
  esac
done
printf '%s\n' "$operation" >> "$GC_HANDOFF_LOG"
if [ "$GC_HANDOFF_SLEEP" = "1" ]; then sleep 1; fi
stopped_file="${GC_HANDOFF_STOPPED_FILE:-$scope/.gc-handoff-stopped}"
result=eligible
mutates=false
error_code=""
if [ "$operation" = "handoff-inspect" ] && [ -f "$stopped_file" ]; then
  result=refused
  error_code=process_missing
fi
if [ "$GC_HANDOFF_REFUSE" = "1" ]; then
  result=refused
  error_code=process_unowned
fi
if [ "$operation" = "handoff-stop" ]; then
  result=stopped
  mutates=true
  if [ "$GC_HANDOFF_STOP_LEAVES_LIVE" != "1" ]; then : > "$stopped_file"; fi
fi
if [ -n "$socket" ]; then
  endpoint=$(printf '{"host":"","port":0,"socket":"%s"}' "$socket")
else
  endpoint=$(printf '{"host":"%s","port":%s,"socket":""}' "$host" "$port")
fi
if [ -n "$GC_HANDOFF_DATABASE" ]; then database="$GC_HANDOFF_DATABASE"; fi
if [ -n "$GC_HANDOFF_TOKEN" ]; then token="$GC_HANDOFF_TOKEN"; else token="sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"; fi
printf '{"schema_version":1,"operation":"%s","result":"%s","owner":"legacy-gc","mutates":%s,"identity":{"city_root":"%s","scope_root":"%s","database":"%s","workspace":"%s","endpoint":%s,"data_dir":"%s/.gc/data","config_file":"%s/.gc/config","pid":%s,"start_identity":"fake","start_time_ticks":1,"port_holder_pid":%s},"identity_token":"%s","error_code":"%s"}\n' "$operation" "$result" "$mutates" "$city" "$scope" "$database" "$workspace" "$endpoint" "$city" "$city" "$$" "$$" "$token" "$error_code"
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
