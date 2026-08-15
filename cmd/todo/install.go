package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runInstall wires this binary into a project's MCP configuration, so onboarding a project is one
// command instead of hand-writing JSON. It writes (or merges into) `.mcp.json` in the target
// directory — the file Claude Code reads at startup — preserving every other server the file
// already names and replacing only the `todo` entry.
//
// The command path is this very binary, resolved absolute: an MCP host does not inherit the
// caller's PATH, and "todo" bare would work in one shell and fail in the host. The database is the
// XDG default unless --db points somewhere; one database with projects as epics is the intended
// shape, so a per-project file is the exception and asking for it is explicit.
func runInstall(dir, db string, noClaudeMD bool) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve this binary: %w", err)
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}

	entry := map[string]any{"command": self, "args": []string{"mcp"}}
	if len(db) > 0 {
		abs, err := filepath.Abs(db)
		if err != nil {
			return err
		}
		entry["env"] = map[string]string{"TODO_DB": abs}
	}

	path := filepath.Join(dir, ".mcp.json")
	cfg, err := readMCPConfig(path)
	if err != nil {
		return err
	}
	cfg.Servers["todo"] = entry
	if err := writeMCPConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wired the todo MCP server into %s\n", path)

	if !noClaudeMD {
		md := filepath.Join(dir, "CLAUDE.md")
		if err := upsertBlock(md, claudeBlock); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "kept the usage block in %s current\n", md)
	}
	fmt.Fprintln(os.Stderr, "the host reads .mcp.json at startup, so restart the session for the tools to appear")
	return nil
}

// runUninstall removes the todo entry and leaves everything else exactly as it was. The file stays
// even when it ends up empty — it is the user's file, and an empty server list is a fact worth
// keeping distinct from "never configured".
func runUninstall(dir string) error {
	path := filepath.Join(dir, ".mcp.json")
	cfg, err := readMCPConfig(path)
	if err != nil {
		return err
	}
	if _, ok := cfg.Servers["todo"]; !ok {
		fmt.Fprintf(os.Stderr, "%s carries no todo entry; nothing to do\n", path)
	} else {
		delete(cfg.Servers, "todo")
		if err := writeMCPConfig(path, cfg); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "removed the todo entry from %s\n", path)
	}
	return removeBlock(filepath.Join(dir, "CLAUDE.md"))
}

// The agent-facing instructions install maintains in CLAUDE.md, between markers it owns. The MCP
// tools already self-describe over the protocol, so this stays short: what lives here, which way to
// reach it, and the one convention (a project is an epic) the schema cannot say.
const (
	blockBegin = "<!-- todo-mcp:begin -->"
	blockEnd   = "<!-- todo-mcp:end -->"

	claudeBlock = blockBegin + `
## backlog + wiki — the todo tool

The project's tasks and design docs live in one SQLite backlog served by the ` + "`todo`" + ` MCP server
(wired in ` + "`.mcp.json`" + `). Prefer the MCP tools (` + "`todo_list`, `todo_ready`, `doc_list`, `doc_show`" + `,
...) — they return structured data; the ` + "`todo`" + ` CLI answers the same questions in a shell
(` + "`todo ready`, `todo doc list --search <q>`, `todo docs <task-id>`" + `). A project is an EPIC, not a
separate database; TAGS are free labels for slicing, and the ` + "`tag`" + ` filter takes a comma list a
task must carry all of (` + "`todo_list {tag: \"ee,scheduler\"}`" + `). Record finished work with
` + "`todo_done`" + ` and map commits with ` + "`sync_commits`" + `; deletion is always soft (trash + restore),
so nothing is lost to a wrong call.
` + blockEnd + "\n"
)

// upsertBlock keeps exactly one copy of the block in the file: replaces it between the markers when
// they are present, appends it otherwise, creates the file when there is none. Everything outside
// the markers is the user's and is carried through byte for byte.
func upsertBlock(path, block string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return os.WriteFile(path, []byte(block), 0o644)
	}
	if err != nil {
		return err
	}
	s := string(b)
	if i := indexBlock(s); i >= 0 {
		j := indexBlockEnd(s)
		if j < i {
			return fmt.Errorf("%s: begin marker without an end marker; not touching the file", path)
		}
		return os.WriteFile(path, []byte(s[:i]+block+s[j:]), 0o644)
	}
	if len(s) > 0 && s[len(s)-1] != '\n' {
		s += "\n"
	}
	return os.WriteFile(path, []byte(s+"\n"+block), 0o644)
}

// removeBlock deletes the marked block and nothing else; a file without one is left alone.
func removeBlock(path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	s := string(b)
	i := indexBlock(s)
	if i < 0 {
		return nil
	}
	j := indexBlockEnd(s)
	if j < i {
		return fmt.Errorf("%s: begin marker without an end marker; not touching the file", path)
	}
	out := s[:i] + s[j:]
	fmt.Fprintf(os.Stderr, "removed the usage block from %s\n", path)
	return os.WriteFile(path, []byte(out), 0o644)
}

func indexBlock(s string) int { return strings.Index(s, blockBegin) }
func indexBlockEnd(s string) int {
	i := strings.Index(s, blockEnd)
	if i < 0 {
		return i
	}
	end := i + len(blockEnd)
	if end < len(s) && s[end] == '\n' {
		end++
	}
	return end
}

// mcpConfig is the slice of .mcp.json this tool owns an opinion about. Everything else in the file
// is carried through untouched — the file belongs to the project, not to this tool, and clobbering
// a co-resident server would be exactly the damage install exists to avoid.
type mcpConfig struct {
	Servers map[string]any
	rest    map[string]json.RawMessage
}

func readMCPConfig(path string) (mcpConfig, error) {
	cfg := mcpConfig{Servers: map[string]any{}, rest: map[string]json.RawMessage{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg.rest); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if raw, ok := cfg.rest["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &cfg.Servers); err != nil {
			return cfg, fmt.Errorf("%s: mcpServers: %w", path, err)
		}
	}
	return cfg, nil
}

func writeMCPConfig(path string, cfg mcpConfig) error {
	raw, err := json.Marshal(cfg.Servers)
	if err != nil {
		return err
	}
	cfg.rest["mcpServers"] = raw
	out, err := json.MarshalIndent(cfg.rest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}
