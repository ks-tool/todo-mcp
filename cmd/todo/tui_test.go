package main

import (
	"path/filepath"
	"testing"

	"github.com/ks-tool/todo-mcp/internal/todo"
)

// TestReloadSurvivesModeSwitches pins the crash a doc link found in production: bubbles renders a
// row by indexing the COLUMNS with the row's own cell count, so switching between the 4-column task
// table and the 3-column doc table with the other mode's rows still present panicked inside
// SetColumns. reload must therefore clear the rows before reshaping — and this test walks every
// transition without a terminal, because the row rendering happens in SetColumns itself.
func TestReloadSurvivesModeSwitches(t *testing.T) {
	st, err := todo.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.Put(todo.Task{ID: "x-01", Epic: "X", Status: todo.StatusOpen, Text: "a", Tags: []string{"one"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutDoc(todo.Doc{ID: "doc-y", Path: "y", Title: "Y", Body: "b"}); err != nil {
		t.Fatal(err)
	}

	m := newTUI(st)
	m.width, m.height = 100, 30
	m.layout()

	// Every transition the keys can produce, in both directions; a panic fails the test.
	m.reload() // tasks
	m.mode = modeDocs
	m.reload() // tasks -> docs (the crash the trace showed)
	m.mode = modeTasks
	m.reload() // docs -> tasks (the same bug, mirrored)
	m.trash = true
	m.reload() // empty trash
	m.trash = false
	m.tags = []string{"one"}
	m.reload() // filtered
	m.mode = modeDocs
	m.reload() // filtered tasks -> docs
}
