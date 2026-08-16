package main

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ks-tool/todo-mcp/internal/todo"
)

func tuiWith(t *testing.T, tasks ...todo.Task) *tui {
	t.Helper()
	st, err := todo.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, task := range tasks {
		if err := st.Put(task); err != nil {
			t.Fatal(err)
		}
	}
	m := newTUI(st)
	m.width, m.height = 120, 30
	m.layout()
	m.reload()
	return m
}

func key(s string) tea.KeyMsg {
	if s == "esc" {
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestEpicField covers todomcp-09: the epic is chosen from the ones that exist, with a sentinel to
// create a new one; the current epic pre-selects, a new task defaults to the first existing epic,
// and an epic not yet in the backlog is added as its own option so it can still be shown.
func TestEpicField(t *testing.T) {
	t.Setenv("TODO_EPICS", "") // no project root, so the default falls to the first existing epic
	m := tuiWith(t,
		todo.Task{ID: "a-01", Epic: "alpha", Text: "x"},
		todo.Task{ID: "b-01", Epic: "beta", Text: "y"},
	)
	sel, opts := m.epicField("beta")
	if sel != "beta" {
		t.Errorf("an existing epic must pre-select, got %q", sel)
	}
	if len(opts) != 3 { // alpha, beta, sentinel
		t.Errorf("want alpha+beta+sentinel, got %d options", len(opts))
	}
	if last := opts[len(opts)-1]; last.Value != newEpicSentinel {
		t.Errorf("the last option must be the new-epic sentinel, got %q", last.Value)
	}
	if sel, _ := m.epicField(""); sel != "alpha" {
		t.Errorf("a new task with no root epic must default to the first, got %q", sel)
	}
	if sel, opts := m.epicField("gamma"); sel != "gamma" || len(opts) != 4 {
		t.Errorf("an epic not in the backlog must be shown as its own option: sel=%q n=%d", sel, len(opts))
	}
}

// TestEscClosesModal covers todomcp-10: Esc cancels any modal, closing it without saving.
func TestEscClosesModal(t *testing.T) {
	m := tuiWith(t, todo.Task{ID: "a-01", Epic: "a", Text: "x"})
	m.Update(key("a")) // open the add form
	if m.focus != focusForm || m.form == nil {
		t.Fatalf("the add form did not open; focus=%v", m.focus)
	}
	before, _ := m.store.List(todo.Filter{})
	m.Update(key("esc"))
	if m.focus != focusList || m.form != nil {
		t.Fatalf("Esc must close the modal; focus=%v form=%v", m.focus, m.form != nil)
	}
	after, _ := m.store.List(todo.Filter{})
	if len(after) != len(before) {
		t.Errorf("Esc must not save a task: %d before, %d after", len(before), len(after))
	}
}

// TestCommentFormOpens covers todomcp-08: 'm' on a selected task opens a modal, and it opens even on
// a done task — a comment is settable after close.
func TestCommentFormOpens(t *testing.T) {
	m := tuiWith(t, todo.Task{ID: "d-01", Epic: "d", Status: todo.StatusDone, Text: "shipped"})
	m.statusF = "" // show any status, so the done task is visible
	m.reload()
	m.Update(key("m"))
	if m.focus != focusForm || m.form == nil {
		t.Fatalf("pressing m must open the comment form; focus=%v form=%v", m.focus, m.form != nil)
	}
}
