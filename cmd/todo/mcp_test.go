package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ks-tool/todo-mcp/internal/todo"
)

// TestMCPServerAnswersOverStdio drives the real MCP server the way an LLM host would: it builds the
// binary, seeds a database, launches `todo mcp`, and speaks the protocol over the process's stdio
// with the SDK's own client. It asserts the tools are advertised and that a read and a write both
// come back structured — the whole point of the front is that a host gets data, not text to parse.
func TestMCPServerAnswersOverStdio(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "todo")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	// A database with one known task to fetch.
	db := filepath.Join(dir, "backlog.db")
	st, err := todo.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Put(todo.Task{ID: "ee-x-01", Epic: "X", Status: todo.StatusOpen, Priority: "P1", Text: "seeded"})
	_ = st.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.Command(bin, "mcp")
	cmd.Env = append(os.Environ(), "TODO_DB="+db, "TODO_EPICS=rooted, second")
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal("connect:", err)
	}
	defer func() { _ = session.Close() }()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal("list tools:", err)
	}
	if len(tools.Tools) < 10 {
		t.Fatalf("want the full tool set, got %d", len(tools.Tools))
	}

	// A read: show the seeded task, and check the structured payload carries it.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "todo_show", Arguments: map[string]any{"id": "ee-x-01"},
	})
	if err != nil {
		t.Fatal("call todo_show:", err)
	}
	var out struct {
		Task *todo.Task `json:"task"`
	}
	mustDecode(t, res, &out)
	if out.Task == nil || out.Task.ID != "ee-x-01" || out.Task.Priority != "P1" {
		t.Fatalf("todo_show returned the wrong task: %+v", out.Task)
	}

	// A write: add a task, then confirm it is really in the store by asking for it back.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "todo_add", Arguments: map[string]any{"tags": []string{"ee"}, "epic": "X", "priority": "P3", "text": "added via mcp"},
	})
	if err != nil {
		t.Fatal("call todo_add:", err)
	}
	var added struct {
		Task *todo.Task `json:"task"`
	}
	mustDecode(t, res, &added)
	if added.Task == nil || added.Task.ID != "x-01" {
		t.Fatalf("todo_add minted the wrong id (epic-only now): %+v", added.Task)
	}
	// The result is the task as PERSISTED, not as sent: Put derives the rank from the priority, and
	// the caller must see the derived value rather than the zero the input struct carried.
	if added.Task.Rank != 3 {
		t.Fatalf("todo_add returned the pre-write struct: priority P3 must come back with rank 3, got %d", added.Task.Rank)
	}

	// An add WITHOUT an epic lands in the project's root epic — the FIRST of the TODO_EPICS list
	// install writes into the server's environment — rather than erroring, minting an epic of its
	// own, or picking any other entry of the list.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "todo_add", Arguments: map[string]any{"text": "epicless add"},
	})
	if err != nil {
		t.Fatal("call todo_add without epic:", err)
	}
	added.Task = nil
	mustDecode(t, res, &added)
	if added.Task == nil || added.Task.Epic != "rooted" {
		t.Fatalf("an epicless add must land in the first of TODO_EPICS, got %+v", added.Task)
	}

	// The wiki write path end to end: create a page, edit ONE field, and check the others rode
	// through — over MCP an omitted field means "leave it", and that contract is what this pins.
	if _, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "doc_add", Arguments: map[string]any{"path": "auth/page", "kind": "design", "body": "the body"},
	}); err != nil {
		t.Fatal("call doc_add:", err)
	}
	if _, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "doc_edit", Arguments: map[string]any{"id": "doc-auth-page", "kind": "threat-model"},
	}); err != nil {
		t.Fatal("call doc_edit:", err)
	}
	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "doc_show", Arguments: map[string]any{"id": "doc-auth-page"},
	})
	if err != nil {
		t.Fatal("call doc_show:", err)
	}
	var shown struct {
		Doc *todo.Doc `json:"doc"`
	}
	mustDecode(t, res, &shown)
	if shown.Doc == nil || shown.Doc.Kind != "threat-model" || shown.Doc.Body != "the body" || shown.Doc.Path != "auth/page" {
		t.Fatalf("doc_edit must change only the field given: %+v", shown.Doc)
	}
}

// mustDecode reads the structured content of a tool result into v.
func mustDecode(t *testing.T, res *mcp.CallToolResult, v any) {
	t.Helper()
	if res.IsError {
		msg, _ := json.Marshal(res.Content)
		t.Fatalf("tool returned an error: %s", msg)
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode structured content %s: %v", b, err)
	}
}
