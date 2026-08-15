package main

import (
	"path/filepath"
	"strings"
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

// TestSortByColumn pins the sorting contract: a digit orders by that column, the same digit flips
// the direction, PRI orders by rank rather than by the label's spelling (P2 before EE even though
// "EE" < "P2" as strings), and a task-table column that the doc table does not have is inert there
// rather than out of range.
func TestSortByColumn(t *testing.T) {
	st, err := todo.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for _, task := range []todo.Task{
		{ID: "b-01", Epic: "B", Status: todo.StatusOpen, Priority: "EE", Text: "x"},
		{ID: "a-01", Epic: "A", Status: todo.StatusOpen, Priority: "P2", Text: "y"},
	} {
		if err := st.Put(task); err != nil {
			t.Fatal(err)
		}
	}

	m := newTUI(st)
	m.width, m.height = 100, 30
	m.layout()
	m.reload()

	m.sortCol, m.sortAsc = 0, true // ID ascending
	m.resort()
	if m.tasks[0].ID != "a-01" {
		t.Fatalf("ID asc: got %s first", m.tasks[0].ID)
	}
	m.sortAsc = false
	m.resort()
	if m.tasks[0].ID != "b-01" {
		t.Fatalf("ID desc: got %s first", m.tasks[0].ID)
	}
	m.sortCol, m.sortAsc = 1, true // PRI by rank: P2 (2) before EE (7)
	m.resort()
	if m.tasks[0].Priority != "P2" {
		t.Fatalf("PRI asc must rank P2 before EE, got %s first", m.tasks[0].Priority)
	}

	m.mode, m.sortCol = modeDocs, 3 // a column the 3-column doc table does not have
	m.reload()
}

// TestFrameFitsAndKeepsHeaders renders the real frame and holds it to the terminal: bubbletea trims
// an oversized frame from the TOP, so a frame even one line too tall shows everything EXCEPT the
// column titles — which is precisely the bug this pins, measured on a live terminal.
func TestFrameFitsAndKeepsHeaders(t *testing.T) {
	st, err := todo.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.Put(todo.Task{ID: "x-01", Epic: "X", Status: todo.StatusOpen, Priority: "P1", Text: "a"}); err != nil {
		t.Fatal(err)
	}

	m := newTUI(st)
	m.width, m.height = 100, 30
	m.layout()
	m.reload()

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) > m.height {
		t.Fatalf("the list frame is %d lines on a %d-line terminal; the top row would be trimmed", len(lines), m.height)
	}
	if !strings.Contains(lines[0], "ID") || !strings.Contains(lines[0], "PRI") {
		t.Fatalf("the column titles are not on the first line: %q", lines[0])
	}

	m.openViewer()
	if got := len(strings.Split(m.View(), "\n")); got > m.height {
		t.Fatalf("the viewer frame is %d lines on a %d-line terminal", got, m.height)
	}
}
