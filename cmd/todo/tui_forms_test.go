package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestCtrlRRefreshesFilteredList covers todomcp-11: a task added elsewhere while a filter is applied
// is not in the list until ctrl+r re-reads it, and the selection is kept.
func TestCtrlRRefreshesFilteredList(t *testing.T) {
	m := tuiWith(t, todo.Task{ID: "a-01", Epic: "a", Status: todo.StatusOpen, Text: "one", Tags: []string{"x"}})
	m.tags = []string{"x"}
	m.reload()
	if len(m.tasks) != 1 {
		t.Fatalf("filtered list should hold 1, got %d", len(m.tasks))
	}
	sel, _ := m.selectedTask()

	// A second matching task appears in the store, bypassing the TUI entirely.
	if err := m.store.Put(todo.Task{ID: "a-02", Epic: "a", Status: todo.StatusOpen, Text: "two", Tags: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if len(m.tasks) != 1 {
		t.Error("an external add must not appear until a refresh")
	}
	m.Update(key("ctrl+r"))
	if len(m.tasks) != 2 {
		t.Errorf("ctrl+r must pull in the new task under the filter, got %d", len(m.tasks))
	}
	if cur, ok := m.selectedTask(); !ok || cur.ID != sel.ID {
		t.Errorf("refresh must keep the selection on %s, got %v/%q", sel.ID, ok, cur.ID)
	}
}

// TestHelpAndStatusLineFits covers todomcp-19: '?' opens the full key help in the viewer, the rare
// keys live there (not in the status line), and the status line never exceeds the terminal width.
func TestHelpAndStatusLineFits(t *testing.T) {
	m := tuiWith(t, todo.Task{ID: "a-01", Epic: "a", Status: todo.StatusOpen, Text: "x"})

	// '?' opens help.
	m.Update(key("?"))
	if m.focus != focusViewer || !m.helpOpen {
		t.Fatalf("? must open the help viewer; focus=%v helpOpen=%v", m.focus, m.helpOpen)
	}

	// The status line stays within the terminal width even on a narrow screen.
	for _, w := range []int{60, 80, 100} {
		m.focus, m.helpOpen = focusList, false
		m.width, m.height = w, 30
		m.layout()
		m.reload()
		if got := lipgloss.Width(m.statusLine()); got > w {
			t.Errorf("status line is %d wide on a %d-column terminal", got, w)
		}
		// The rare keys must NOT be in the status line — they belong to help.
		for _, rare := range []string{"commit", "comment", "sort", "trash"} {
			if strings.Contains(m.statusLine(), rare) {
				t.Errorf("rare key %q must not be in the status line at width %d", rare, w)
			}
		}
	}
	// …but they ARE in the help text.
	for _, rare := range []string{"commit", "comment", "sort", "trash"} {
		if !strings.Contains(helpText, rare) {
			t.Errorf("help must document %q", rare)
		}
	}
}

// TestAddEpicModalFitsTerminal covers todomcp-15: the add flow picks the epic in its own modal
// whose list is height-capped, so even many epics stay inside the terminal instead of overflowing.
func TestAddEpicModalFitsTerminal(t *testing.T) {
	var tasks []todo.Task
	for i := 0; i < 40; i++ {
		tasks = append(tasks, todo.Task{ID: fmt.Sprintf("e%02d-01", i), Epic: fmt.Sprintf("epic-%02d", i), Text: "x"})
	}
	m := tuiWith(t, tasks...)
	m.Update(key("a")) // opens the dedicated epic-pick modal, not the field form
	if m.focus != focusForm || m.form == nil {
		t.Fatalf("the add epic modal did not open; focus=%v", m.focus)
	}
	if n := len(strings.Split(m.View(), "\n")); n > m.height {
		t.Fatalf("the epic modal is %d lines on a %d-line terminal — it overflows", n, m.height)
	}
}

// TestEditFormEpicReadOnly covers todomcp-15: an edit cannot move a task between epics — the epic is
// shown in the title, not as an editable list, and the other epics never appear.
func TestEditFormEpicReadOnly(t *testing.T) {
	m := tuiWith(t,
		todo.Task{ID: "a-01", Epic: "alpha", Text: "x"},
		todo.Task{ID: "b-01", Epic: "beta", Text: "y"},
	)
	sel, ok := m.selectedTask()
	if !ok {
		t.Fatal("no task selected after reload")
	}
	m.Update(key("e")) // edit the selected task
	if m.focus != focusForm || m.form == nil {
		t.Fatalf("the edit form did not open; focus=%v", m.focus)
	}
	view := m.View()
	if !strings.Contains(view, "epic: "+sel.Epic) {
		t.Errorf("the edit form must show the current epic in the title:\n%s", view)
	}
	other := "beta"
	if sel.Epic == "beta" {
		other = "alpha"
	}
	if strings.Contains(view, other) {
		t.Errorf("the edit form must not list the other epic %q:\n%s", other, view)
	}
	if n := len(strings.Split(view, "\n")); n > m.height {
		t.Fatalf("the edit form is %d lines on a %d-line terminal", n, m.height)
	}
}

// TestCommentThreadInDetail covers todomcp-14's TUI side: the detail pane shows a task's comment
// thread, and pressing 'm' opens the add-comment modal.
func TestCommentThreadInDetail(t *testing.T) {
	m := tuiWith(t, todo.Task{ID: "c-01", Epic: "c", Status: todo.StatusOpen, Text: "x"})
	if _, err := m.store.AddComment("c-01", "a threaded remark", "2026-08-16T10:00:00Z"); err != nil {
		t.Fatal(err)
	}
	m.reload()
	body := m.detailContent()
	if !strings.Contains(body, "comments") || !strings.Contains(body, "a threaded remark") {
		t.Errorf("detail pane must show the comment thread:\n%s", body)
	}
	m.Update(key("m"))
	if m.focus != focusForm || m.form == nil {
		t.Errorf("m must open the add-comment modal; focus=%v", m.focus)
	}
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

// TestEpicFilterNarrows covers todomcp-13: the TUI filter takes an epic, threaded into reload, and
// 'p' opens the picker.
func TestEpicFilterNarrows(t *testing.T) {
	m := tuiWith(t,
		todo.Task{ID: "a-01", Epic: "alpha", Status: todo.StatusOpen, Text: "x"},
		todo.Task{ID: "b-01", Epic: "beta", Status: todo.StatusOpen, Text: "y"},
	)
	if len(m.tasks) != 2 {
		t.Fatalf("want 2 tasks unfiltered, got %d", len(m.tasks))
	}
	m.epicF = "alpha"
	m.reload()
	if len(m.tasks) != 1 || m.tasks[0].Epic != "alpha" {
		t.Errorf("the epic filter must narrow to alpha, got %d", len(m.tasks))
	}
	m.epicF = ""
	m.reload()
	m.Update(key("p"))
	if m.focus != focusForm || m.form == nil {
		t.Errorf("p must open the epic filter modal; focus=%v", m.focus)
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
