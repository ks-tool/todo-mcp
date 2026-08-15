package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallMergesAndPreservesNeighbours pins the property install exists for: a project's
// .mcp.json may already name other servers, and wiring todo in must not touch them — clobbering a
// neighbour is exactly the damage a hand edit risks and this command must not. Top-level keys other
// than mcpServers ride through too.
func TestInstallMergesAndPreservesNeighbours(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	existing := `{
  "mcpServers": {
    "other": {"command": "/usr/bin/other", "args": ["serve"], "env": {"X": "1"}}
  },
  "somebodyElses": {"keep": true}
}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := readMCPConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Servers["todo"] = map[string]any{"command": "/bin/todo", "args": []string{"mcp"}}
	if err := writeMCPConfig(path, cfg); err != nil {
		t.Fatal(err)
	}

	var out map[string]json.RawMessage
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["somebodyElses"]; !ok {
		t.Error("a top-level key that is not mcpServers was dropped")
	}
	var servers map[string]map[string]any
	if err := json.Unmarshal(out["mcpServers"], &servers); err != nil {
		t.Fatal(err)
	}
	if _, ok := servers["other"]; !ok {
		t.Error("a co-resident server was clobbered")
	}
	if servers["other"]["command"] != "/usr/bin/other" {
		t.Errorf("the neighbour's command changed: %v", servers["other"]["command"])
	}
	if _, ok := servers["todo"]; !ok {
		t.Error("the todo entry was not written")
	}

	// And uninstall takes only its own entry back out.
	cfg, err = readMCPConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	delete(cfg.Servers, "todo")
	if err := writeMCPConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	// Fresh maps: Unmarshal into a non-empty map MERGES, and the stale "todo" key from the check
	// above would mask exactly the regression this half of the test exists to catch.
	out, servers = nil, nil
	b, _ = os.ReadFile(path)
	_ = json.Unmarshal(b, &out)
	_ = json.Unmarshal(out["mcpServers"], &servers)
	if _, ok := servers["todo"]; ok {
		t.Error("uninstall left the todo entry behind")
	}
	if _, ok := servers["other"]; !ok {
		t.Error("uninstall removed a neighbour")
	}
}

// TestClaudeBlockIsOwnedBetweenMarkers pins the contract of the CLAUDE.md half of install: exactly
// one copy of the block, replaced in place on a second install — a renamed root epic included —
// everything outside the markers carried byte for byte, and removal taking only the block.
func TestClaudeBlockIsOwnedBetweenMarkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")

	// No file: created holding just the block, naming the project's epic.
	if err := upsertBlock(path, claudeBlock("myproj")); err != nil {
		t.Fatal(err)
	}
	// A user writes around it.
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "`myproj` epic") {
		t.Fatalf("the block does not name the root epic:\n%s", b)
	}
	content := "# my own header\n\n" + string(b) + "\nmy own footer\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// A second install replaces between the markers — the epic with it — and keeps the user's text.
	if err := upsertBlock(path, claudeBlock("renamed")); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	s := string(b)
	if got := strings.Count(s, blockBegin); got != 1 {
		t.Fatalf("want exactly one block after re-install, got %d\n%s", got, s)
	}
	if !strings.Contains(s, "# my own header") || !strings.Contains(s, "my own footer") {
		t.Error("re-install disturbed the user's own text")
	}
	if strings.Contains(s, "myproj") || !strings.Contains(s, "`renamed` epic") {
		t.Error("re-install must replace the epic name, not keep the old block")
	}

	// Removal takes the block and nothing else.
	if err := removeBlock(path); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	s = string(b)
	if strings.Contains(s, blockBegin) {
		t.Error("the block survived removal")
	}
	if !strings.Contains(s, "# my own header") || !strings.Contains(s, "my own footer") {
		t.Error("removal disturbed the user's own text")
	}

	// A begin marker with no end is somebody's half-edit: refuse rather than eat to EOF.
	if err := os.WriteFile(path, []byte("x\n"+blockBegin+"\norphan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := upsertBlock(path, claudeBlock("myproj")); err == nil {
		t.Error("an orphaned begin marker must refuse the upsert")
	}
}

// TestDBResolutionOrder pins the one fixed order: --db, then TODO_DB, then a backlog.db in the
// current directory, then the XDG default. Each step is checked by knocking the one above it away.
func TestDBResolutionOrder(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("backlog.db", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TODO_DB", "/env/wins.db")

	flagDB = "/flag/wins.db"
	t.Cleanup(func() { flagDB = "" })
	if got := resolveDB(); got != "/flag/wins.db" {
		t.Fatalf("--db must win over everything, got %q", got)
	}
	flagDB = ""
	if got := resolveDB(); got != "/env/wins.db" {
		t.Fatalf("TODO_DB must win over the local file, got %q", got)
	}
	t.Setenv("TODO_DB", "")
	if got := resolveDB(); got != "backlog.db" {
		t.Fatalf("a backlog.db in the current directory must win over the default, got %q", got)
	}
	if err := os.Remove("backlog.db"); err != nil {
		t.Fatal(err)
	}
	xdg := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_DATA_HOME", xdg)
	if got := resolveDB(); got != filepath.Join(xdg, "todo", "backlog.db") {
		t.Fatalf("with nothing else the XDG default answers, got %q", got)
	}
}
