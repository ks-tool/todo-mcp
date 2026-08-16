package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/ks-tool/todo-mcp/internal/todo"
)

// newEpicSentinel is the epic-select option that means "create a new one", its name taken from the
// input field beside it.
const newEpicSentinel = "＋ new epic…"

// uiForm is one modal interaction: a huh form plus what to do when it completes. huh forms are
// bubbletea models themselves, so update delegates to them and watches the state — completed runs
// save, aborted (Esc) just closes, and either way the list reloads to show the truth.
type uiForm struct {
	form *huh.Form
	save func()
}

// openForm activates a form and returns its Init command, which the CALLER must hand back to the
// program: huh drives its own field focus through commands, and a discarded Init is a form that
// never focuses its first field.
func (m *tui) openForm(f *huh.Form, save func()) tea.Cmd {
	m.form = &uiForm{form: f, save: save}
	m.focus = focusForm
	return f.Init()
}

func (m *tui) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.form == nil {
		m.focus = focusList
		return m, nil
	}
	// Esc cancels ANY modal, uniformly — the same way ctrl+c aborts a huh form, but a key a person
	// actually reaches for. Intercepted here rather than left to each form, so add, edit, commit,
	// comment, the filters and every future modal close the same way, without saving.
	if key, ok := msg.(tea.KeyMsg); ok && key.Type == tea.KeyEsc {
		m.form = nil
		m.focus = focusList
		return m, nil
	}
	model, cmd := m.form.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		m.form.form = f
	}
	switch m.form.form.State {
	case huh.StateCompleted:
		m.form.save()
		m.form = nil
		m.focus = focusList
		m.reload()
		return m, nil
	case huh.StateAborted:
		m.form = nil
		m.focus = focusList
		return m, nil
	}
	return m, cmd
}

func (m *tui) View_FormActive() bool { return m.focus == focusForm && m.form != nil }

// openTaskForm edits t, or creates a new task when t is nil. The id is minted on save, never
// asked for.
func (m *tui) openTaskForm(t *todo.Task) tea.Cmd {
	var cur todo.Task
	if t != nil {
		cur = *t
	} else if len(m.tags) > 0 {
		cur.Tags = append([]string(nil), m.tags...) // the active filter is the natural default for a new task
	}
	tags := joinComma(cur.Tags)
	pri, dep, text := cur.Priority, cur.DepText, cur.Text

	// The epic is CHOSEN from the ones that exist, not typed — a free-text epic is how a project ends
	// up with two spellings of the same thing. A "＋ new epic…" option, with the input beside it,
	// keeps creating one in the same window.
	epicSel, newEpicOpts := m.epicField(cur.Epic)
	newEpicName := ""

	// slug and touchpoints are not edited here — they are path/linking metadata, kept off the human
	// form; an edit carries the stored values through untouched (the CLI is where they change).
	f := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("epic").Options(newEpicOpts...).Value(&epicSel),
		huh.NewInput().Title("new epic (used if ＋ new epic… is chosen)").Value(&newEpicName),
		huh.NewInput().Title("tags (comma, optional)").Value(&tags),
		huh.NewInput().Title("priority").Value(&pri),
		huh.NewInput().Title("dep").Value(&dep),
		huh.NewText().Title("text").Value(&text).Lines(8),
	))
	return m.openForm(f, func() {
		epic := epicSel
		if epic == newEpicSentinel {
			epic = strings.TrimSpace(newEpicName)
		}
		if len(epic) == 0 || len(text) == 0 {
			m.flash = "epic and text are required"
			return
		}
		edited := todo.Task{
			Epic: epic, Tags: splitComma(tags), Priority: pri, Slug: cur.Slug,
			Touch: cur.Touch, DepText: dep, Text: text,
			Status: cur.Status,
		}
		if len(edited.Status) == 0 {
			edited.Status = todo.StatusOpen
		}
		if t != nil {
			edited.ID, edited.Order = t.ID, t.Order
		} else {
			id, err := m.store.NextID(epic)
			if err != nil {
				m.flash = err.Error()
				return
			}
			edited.ID = id
		}
		if err := m.store.Put(edited); err != nil {
			m.flash = err.Error()
		}
	})
}

// epicField builds the epic selector for a task form: the epics that exist, plus a sentinel to
// create one, with the current epic (or, for a new task, the project root) pre-selected. It returns
// the pre-selection and the options.
func (m *tui) epicField(current string) (string, []huh.Option[string]) {
	epics, _ := m.store.Epics()
	sel := current
	if len(sel) == 0 {
		if r := rootEpic(); len(r) > 0 {
			sel = r
		} else if len(epics) > 0 {
			sel = epics[0]
		} else {
			sel = newEpicSentinel
		}
	}
	opts := make([]huh.Option[string], 0, len(epics)+2)
	if sel != newEpicSentinel && !contains(epics, sel) {
		opts = append(opts, huh.NewOption(sel, sel)) // a current/default epic not yet in the backlog
	}
	for _, e := range epics {
		opts = append(opts, huh.NewOption(e, e))
	}
	opts = append(opts, huh.NewOption(newEpicSentinel, newEpicSentinel))
	return sel, opts
}

func (m *tui) openDocForm(d *todo.Doc) tea.Cmd {
	var cur todo.Doc
	if d != nil {
		cur = *d
	} else {
		cur.Kind = "note"
	}
	path, title, kind, body := cur.Path, cur.Title, cur.Kind, cur.Body

	f := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("path (the slug tasks match; section/page groups)").Value(&path),
		huh.NewInput().Title("title").Value(&title),
		huh.NewSelect[string]().Title("kind").
			Options(huh.NewOptions("note", "design", "threat-model", "adr", "reference")...).Value(&kind),
		huh.NewText().Title("body (markdown)").Value(&body).Lines(12),
	))
	return m.openForm(f, func() {
		if len(path) == 0 || len(body) == 0 {
			m.flash = "path and body are required"
			return
		}
		out := todo.Doc{Path: path, Title: firstNonEmpty(title, path), Kind: kind, Body: body}
		if d != nil {
			out.ID = d.ID
		} else {
			out.ID = todo.DocID(path)
		}
		if err := m.store.PutDoc(out); err != nil {
			m.flash = err.Error()
		}
	})
}

// openCommentForm edits a task's comment (its done_note). It works on a DONE task without reopening
// it — SetNote never touches status — so a note can be added or corrected after the work shipped.
func (m *tui) openCommentForm(t todo.Task) tea.Cmd {
	note := t.DoneNote
	f := huh.NewForm(huh.NewGroup(
		huh.NewText().Title("comment → " + t.ID).Value(&note).Lines(6),
	))
	return m.openForm(f, func() {
		if _, err := m.store.SetNote(t.ID, note); err != nil {
			m.flash = err.Error()
		}
	})
}

func (m *tui) openCommitForm(t todo.Task) tea.Cmd {
	var sha, subject string
	f := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("sha").Value(&sha),
		huh.NewInput().Title("subject").Value(&subject),
	).Title("commit → " + t.ID))
	return m.openForm(f, func() {
		if len(sha) == 0 {
			m.flash = "a sha is required"
			return
		}
		if err := m.store.Link(t.ID, todo.LinkCommit, sha, subject); err != nil {
			m.flash = err.Error()
		}
	})
}

// openLinkForm maps the task to a doc, picked from the docs that EXIST — a select, not a text
// field, because an id typed free-hand links to nothing and the mistake surfaces later.
func (m *tui) openLinkForm(t todo.Task) tea.Cmd {
	docs, err := m.store.ListDocs("")
	if err != nil || len(docs) == 0 {
		m.flash = "no docs to link; create one in the wiki first (w, then a)"
		return nil
	}
	opts := make([]huh.Option[string], len(docs))
	for i, d := range docs {
		opts[i] = huh.NewOption(d.ID+"  "+oneLine(d.Title, 40), d.ID)
	}
	var docID string
	f := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("link doc → " + t.ID).Options(opts...).Value(&docID),
	))
	return m.openForm(f, func() {
		if err := m.store.Link(t.ID, todo.LinkDoc, docID, ""); err != nil {
			m.flash = err.Error()
		}
	})
}

// openTagFilter is the tag filter as a MULTI-select over the tags the backlog actually uses,
// pre-checked with the current filter. A task can carry several tags, so a single-tag cycler could
// not express "ee AND scheduler"; the chosen set narrows — a task must carry all of it.
func (m *tui) openTagFilter() tea.Cmd {
	all, err := m.store.Tags()
	if err != nil || len(all) == 0 {
		m.flash = "the backlog uses no tags yet"
		return nil
	}
	selected := append([]string(nil), m.tags...)
	opts := make([]huh.Option[string], len(all))
	for i, t := range all {
		opts[i] = huh.NewOption("#"+t, t).Selected(contains(selected, t))
	}
	f := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().Title("filter by tags (space toggles, enter applies)").
			Options(opts...).Value(&selected),
	))
	return m.openForm(f, func() {
		m.tags = selected
	})
}

// allEpicsSentinel is the epic-filter option that clears the filter — every epic.
const allEpicsSentinel = "(all epics)"

// openEpicFilter narrows the list to one epic — the coarse project axis that pairs with the tag
// filter: epic first (which project), then tags (which slice), so the tag list stays legible. The
// epic is picked from the ones that exist, with an option to clear back to all.
func (m *tui) openEpicFilter() tea.Cmd {
	epics, err := m.store.Epics()
	if err != nil || len(epics) == 0 {
		m.flash = "the backlog uses no epics yet"
		return nil
	}
	opts := make([]huh.Option[string], 0, len(epics)+1)
	opts = append(opts, huh.NewOption(allEpicsSentinel, ""))
	for _, e := range epics {
		opts = append(opts, huh.NewOption(e, e))
	}
	sel := m.epicF
	f := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("filter by epic (project)").Options(opts...).Value(&sel),
	))
	return m.openForm(f, func() { m.epicF = sel })
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (m *tui) openConfirm(prompt string, act func()) tea.Cmd {
	var yes bool
	f := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(prompt).Affirmative("delete").Negative("cancel").Value(&yes),
	))
	return m.openForm(f, func() {
		if yes {
			act()
		}
	})
}

func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

func (f *uiForm) View() string { return f.form.View() }
