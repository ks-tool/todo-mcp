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
