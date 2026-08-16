package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"

	"github.com/ks-tool/todo-mcp/internal/todo"
)

// runTUI is the interactive face of the same backlog the CLI reads and the same database it
// writes: a list to move through, a detail pane rendered as real markdown, and forms to add and
// edit. It is a bubbletea program — state in the model, keys become messages, the view is a pure
// function of the state — with glamour rendering the markdown and huh building the forms.
//
// Deletion is soft everywhere: d stamps deleted_at and the task drops into the trash view (t),
// where r restores it. There is no hard delete in the interface at all.
//
// Keys — list: ↑/↓ move · Enter/e edit · a add · d delete · v full-screen view · / search ·
// f cycle the tag filter · s cycle status · t trash · c commit · l link a doc · w wiki ·
// 1–4 sort by that column (the same digit flips the direction, 0 restores the store's order) ·
// Tab focus the detail · q quit. Detail: ↑/↓ scroll · n/p walk links · Enter follow · Tab/Esc
// back. In the trash: r restore.
func runTUI(st *todo.Store) error {
	m := newTUI(st)
	// The background is probed BEFORE the program takes the terminal: inside the alt screen the
	// query cannot be answered, glamour's auto style silently degrades to notty, and every heading
	// renders as plain text. Measured — that was the "## 1. Overview shows as-is" report.
	m.darkBG = lipgloss.HasDarkBackground()
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// focus is which part of the screen owns the keyboard.
type focus int

const (
	focusList focus = iota
	focusDetail
	focusViewer
	focusForm
	focusSearch
)

// uiLink is one followable reference in the detail pane: what it points at and which line of the
// rendered content it sits on, so selection can scroll to it.
type uiLink struct {
	kind, id, label string
	line            int
}

type tui struct {
	store *todo.Store

	mode    mode
	focus   focus
	tasks   []todo.Task
	docs    []todo.Doc
	tags    []string // the tag filter, ALL must match; empty = everything
	statusF todo.Status
	search  string
	trash   bool
	sortCol int  // column the list is ordered by; -1 = the store's own order
	sortAsc bool // direction of that order

	table    table.Model
	detail   viewport.Model
	viewer   viewport.Model
	searchIn textinput.Model
	form     *uiForm

	links   []uiLink
	linkSel int // index into links; -1 = none selected

	width, height int
	flash         string
	darkBG        bool
	mdRender      *glamour.TermRenderer
}

// mode is what the list shows: the backlog or the wiki. One interface, w toggles.
type mode int

const (
	modeTasks mode = iota
	modeDocs
)

func newTUI(st *todo.Store) *tui {
	ti := textinput.New()
	ti.Placeholder = "full-text query; Enter applies, Esc clears"
	return &tui{store: st, statusF: todo.StatusOpen, linkSel: -1, sortCol: -1, searchIn: ti}
}

func (m *tui) Init() tea.Cmd { return nil }

// reload re-reads the store under the current filters. Every mutation ends here, so the screen is
// always the database rather than a copy that can drift.
func (m *tui) reload() {
	if m.mode == modeDocs {
		ds, err := m.store.ListDocs(m.search)
		if err != nil {
			m.flash = err.Error()
			return
		}
		m.docs = ds
		m.sortDocs()
		rows := make([]table.Row, len(m.docs))
		for i, d := range m.docs {
			rows[i] = table.Row{d.ID, d.Kind, d.Path}
		}
		// Rows out first: bubbles renders a row by indexing the COLUMNS with the row's own cell
		// count, so swapping to 3 doc columns while 4-cell task rows are still in the table panics
		// inside SetColumns. An empty table renders nothing and is safe to reshape.
		m.table.SetRows(nil)
		m.table.SetColumns(m.sortMark(docColumns(m.listWidth())))
		m.table.SetRows(rows)
	} else {
		var err error
		if m.trash {
			m.tasks, err = m.store.Trash()
		} else {
			m.tasks, err = m.store.List(todo.Filter{Status: m.statusF, Tags: m.tags, Search: m.search})
		}
		if err != nil {
			m.flash = err.Error()
			return
		}
		m.sortTasks()
		rows := make([]table.Row, len(m.tasks))
		for i, t := range m.tasks {
			rows[i] = table.Row{t.ID, t.Priority, strings.Join(t.Tags, ","), t.Epic}
		}
		m.table.SetRows(nil)
		m.table.SetColumns(m.sortMark(taskColumns(m.listWidth())))
		m.table.SetRows(rows)
	}
	// The height is set here, not at New: SetHeight subtracts the header as it actually renders —
	// two lines now that the columns and styles exist — so the table comes out exactly as tall as
	// the bordered detail pane beside it.
	m.table.SetHeight(m.paneHeight() + 2)
	if m.table.Cursor() >= len(m.table.Rows()) {
		m.table.SetCursor(0)
	}
	m.rebuildDetail()
}

func (m *tui) selectedTask() (todo.Task, bool) {
	i := m.table.Cursor()
	if m.mode != modeTasks || i < 0 || i >= len(m.tasks) {
		return todo.Task{}, false
	}
	return m.tasks[i], true
}

func (m *tui) selectedDoc() (todo.Doc, bool) {
	i := m.table.Cursor()
	if m.mode != modeDocs || i < 0 || i >= len(m.docs) {
		return todo.Doc{}, false
	}
	return m.docs[i], true
}

func (m *tui) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		m.layout()
		m.reload()
		return m, nil
	}
	// A form gets EVERY message, not just keys: huh moves between its fields through its own
	// internal messages produced by commands, so a dispatcher that forwards only tea.KeyMsg makes
	// the form deaf — Tab and the arrows did nothing, which is exactly how this line was earned.
	if m.focus == focusForm {
		return m.updateForm(msg)
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		m.flash = ""
		switch m.focus {
		case focusSearch:
			return m.updateSearch(key)
		case focusViewer:
			return m.updateViewer(key)
		case focusDetail:
			return m.updateDetail(key)
		default:
			return m.updateList(key)
		}
	}
	return m, nil
}

func (m *tui) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "w":
		if m.mode == modeTasks {
			m.mode = modeDocs
		} else {
			m.mode = modeTasks
		}
		m.trash = false
		m.sortCol = -1 // the columns change meaning with the mode, so the order does not carry over
		m.reload()
	case "tab":
		m.focus = focusDetail
		if len(m.links) > 0 {
			m.linkSel = 0 // a visible cursor from the first keystroke
			m.rebuildDetail()
		}
	case "enter", "e":
		if t, ok := m.selectedTask(); ok {
			return m, m.openTaskForm(&t)
		} else if d, ok := m.selectedDoc(); ok {
			return m, m.openDocForm(&d)
		}
	case "a":
		if m.mode == modeDocs {
			return m, m.openDocForm(nil)
		}
		return m, m.openTaskForm(nil)
	case "d":
		if t, ok := m.selectedTask(); ok && !m.trash {
			return m, m.openConfirm("Move "+t.ID+" to trash?", func() {
				_, _ = m.store.Delete(t.ID, time.Now().Format(time.RFC3339))
			})
		} else if d, ok := m.selectedDoc(); ok {
			return m, m.openConfirm("Move doc "+d.ID+" to trash?", func() {
				_, _ = m.store.DeleteDoc(d.ID, time.Now().Format(time.RFC3339))
			})
		}
	case "r":
		if t, ok := m.selectedTask(); ok && m.trash {
			_, _ = m.store.Restore(t.ID)
			m.reload()
		}
	case "t":
		if m.mode == modeTasks {
			m.trash = !m.trash
			m.reload()
		}
	case "s":
		if m.mode == modeTasks {
			m.statusF = map[todo.Status]todo.Status{todo.StatusOpen: todo.StatusDone, todo.StatusDone: "", "": todo.StatusOpen}[m.statusF]
			m.reload()
		}
	case "f":
		if m.mode == modeTasks {
			return m, m.openTagFilter()
		}
	case "1", "2", "3", "4":
		col := int(msg.String()[0] - '1')
		if m.mode == modeDocs && col > 2 {
			return m, nil
		}
		if col == m.sortCol {
			m.sortAsc = !m.sortAsc
		} else {
			m.sortCol, m.sortAsc = col, true
		}
		m.resort()
	case "0":
		if m.sortCol >= 0 {
			m.sortCol = -1
			m.resort()
		}
	case "/":
		m.searchIn.SetValue(m.search)
		m.searchIn.Focus()
		m.focus = focusSearch
	case "v":
		m.openViewer()
	case "c":
		if t, ok := m.selectedTask(); ok {
			return m, m.openCommitForm(t)
		}
	case "l":
		if t, ok := m.selectedTask(); ok {
			return m, m.openLinkForm(t)
		}
	default:
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		m.rebuildDetail()
		return m, cmd
	}
	return m, nil
}

func (m *tui) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "esc":
		m.focus = focusList
		m.linkSel = -1
		m.rebuildDetail()
	case "n", "p":
		if len(m.links) == 0 {
			return m, nil
		}
		if msg.String() == "n" {
			m.linkSel = (m.linkSel + 1) % len(m.links)
		} else {
			m.linkSel = (m.linkSel - 1 + len(m.links)) % len(m.links)
		}
		m.rebuildDetail()
		m.scrollToLink()
	case "enter":
		if m.linkSel >= 0 && m.linkSel < len(m.links) {
			m.follow(m.links[m.linkSel])
		}
	case "q", "ctrl+c":
		return m, tea.Quit
	default:
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *tui) updateViewer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "v":
		m.focus = focusList
	default:
		var cmd tea.Cmd
		m.viewer, cmd = m.viewer.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *tui) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.search = strings.TrimSpace(m.searchIn.Value())
		m.focus = focusList
		m.reload()
	case "esc":
		m.search = ""
		m.focus = focusList
		m.reload()
	default:
		var cmd tea.Cmd
		m.searchIn, cmd = m.searchIn.Update(msg)
		return m, cmd
	}
	return m, nil
}

// follow acts on a link: a tag becomes the filter, a task is selected in the list (filters cleared
// so it cannot be invisibly absent), a doc switches to the wiki and selects it.
func (m *tui) follow(l uiLink) {
	m.focus = focusList
	m.linkSel = -1
	switch l.kind {
	case "tag":
		// Following a tag ADDS it to the filter — the set narrows, which is what a second tag on a
		// filtered list means; f opens the full multi-select to take any of them back out.
		m.mode, m.trash = modeTasks, false
		if !contains(m.tags, l.id) {
			m.tags = append(m.tags, l.id)
		}
		m.reload()
	case "task":
		m.mode, m.tags, m.search, m.statusF, m.trash = modeTasks, nil, "", "", false
		m.reload()
		for i, t := range m.tasks {
			if t.ID == l.id {
				m.table.SetCursor(i)
				break
			}
		}
		m.rebuildDetail()
	case "doc":
		m.mode, m.search = modeDocs, ""
		m.reload()
		for i, d := range m.docs {
			if d.ID == l.id {
				m.table.SetCursor(i)
				break
			}
		}
		m.rebuildDetail()
	}
}

func (m *tui) openViewer() {
	m.viewer.SetContent(m.detailContent())
	m.viewer.GotoTop()
	m.focus = focusViewer
}

func (m *tui) scrollToLink() {
	if m.linkSel < 0 || m.linkSel >= len(m.links) {
		return
	}
	line := m.links[m.linkSel].line
	if line < m.detail.YOffset {
		m.detail.SetYOffset(line)
	} else if line >= m.detail.YOffset+m.detail.Height {
		m.detail.SetYOffset(line - m.detail.Height + 1)
	}
}

// ---- layout and view ----

func (m *tui) listWidth() int { w := m.width * 2 / 5; return max(w, 30) }
func (m *tui) paneWidth() int { return max(m.width-m.listWidth()-2, 20) }

// paneHeight budgets the vertical space: the pane's two border lines and the status line come out
// of the terminal height. Overshooting is not cosmetic — bubbletea trims an oversized frame from
// the TOP, so a frame one line too tall showed everything except the table's header row.
func (m *tui) paneHeight() int { return max(m.height-3, 5) }

func (m *tui) layout() {
	// No WithHeight here: at New the table has no columns yet, so the option would measure an EMPTY
	// header (one line) where the real one — title plus its bottom rule — is two. reload sets the
	// height after the columns exist.
	m.table = table.New(table.WithFocused(true))
	s := table.DefaultStyles()
	s.Header = s.Header.Bold(true).Foreground(lipgloss.Color("12")).BorderStyle(lipgloss.NormalBorder()).BorderBottom(true)
	s.Selected = s.Selected.Background(lipgloss.Color("237")).Foreground(lipgloss.Color("15"))
	m.table.SetStyles(s)

	m.detail = viewport.New(m.paneWidth(), m.paneHeight())
	m.viewer = viewport.New(m.width-2, m.paneHeight())

	// An explicit style, chosen from the background probed at startup — auto degrades to notty
	// inside the alt screen. The heading prefixes go: glamour's stock styles keep "## " in front
	// of every rendered heading, which reads as unrendered markdown beside a styled body.
	cfg := styles.LightStyleConfig
	if m.darkBG {
		cfg = styles.DarkStyleConfig
	}
	for _, h := range []*ansi.StyleBlock{&cfg.H1, &cfg.H2, &cfg.H3, &cfg.H4, &cfg.H5, &cfg.H6} {
		h.Prefix = ""
	}
	r, err := glamour.NewTermRenderer(glamour.WithStyles(cfg), glamour.WithWordWrap(m.paneWidth()-2))
	if err == nil {
		m.mdRender = r
	}
}

func taskColumns(w int) []table.Column {
	id := max(w/3, 18)
	return []table.Column{{Title: "ID", Width: id}, {Title: "PRI", Width: 4},
		{Title: "TAGS", Width: 10}, {Title: "EPIC", Width: max(w-id-20, 10)}}
}

func docColumns(w int) []table.Column {
	id := max(w/3, 18)
	return []table.Column{{Title: "ID", Width: id}, {Title: "KIND", Width: 9},
		{Title: "PATH", Width: max(w-id-15, 10)}}
}

// sortMark labels the sorted column in its own header — the arrow says both which column orders the
// list and which way — so the order on screen is explained by the screen, not by remembered keys.
func (m *tui) sortMark(cols []table.Column) []table.Column {
	if m.sortCol >= 0 && m.sortCol < len(cols) {
		mark := "↓"
		if m.sortAsc {
			mark = "↑"
		}
		cols[m.sortCol].Title += mark
	}
	return cols
}

// sortTasks orders the visible tasks by the chosen column; -1 keeps the store's order. PRI sorts by
// rank — the number behind the label — so the P scale, E4, EE and v2 interleave the way the backlog
// means them to, not alphabetically.
func (m *tui) sortTasks() {
	if m.sortCol < 0 || m.sortCol > 3 {
		return
	}
	key := func(t todo.Task) string {
		switch m.sortCol {
		case 0:
			return t.ID
		case 1:
			return fmt.Sprintf("%02d %s", t.Rank, t.Priority)
		case 2:
			return strings.Join(t.Tags, ",")
		default:
			return t.Epic
		}
	}
	sort.SliceStable(m.tasks, func(i, j int) bool {
		a, b := key(m.tasks[i]), key(m.tasks[j])
		if m.sortAsc {
			return a < b
		}
		return a > b
	})
}

func (m *tui) sortDocs() {
	if m.sortCol < 0 || m.sortCol > 2 {
		return
	}
	key := func(d todo.Doc) string {
		switch m.sortCol {
		case 0:
			return d.ID
		case 1:
			return d.Kind
		default:
			return d.Path
		}
	}
	sort.SliceStable(m.docs, func(i, j int) bool {
		a, b := key(m.docs[i]), key(m.docs[j])
		if m.sortAsc {
			return a < b
		}
		return a > b
	})
}

// resort re-reads under the new order and keeps the SAME row selected — sorting reorders the list,
// and a cursor pinned to an index would silently land on a different task.
func (m *tui) resort() {
	var id string
	if t, ok := m.selectedTask(); ok {
		id = t.ID
	} else if d, ok := m.selectedDoc(); ok {
		id = d.ID
	}
	m.reload()
	if len(id) == 0 {
		return
	}
	if m.mode == modeDocs {
		for i, d := range m.docs {
			if d.ID == id {
				m.table.SetCursor(i)
				break
			}
		}
	} else {
		for i, t := range m.tasks {
			if t.ID == id {
				m.table.SetCursor(i)
				break
			}
		}
	}
	m.rebuildDetail()
}

var (
	stTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	stDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	stSect   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	stLink   = lipgloss.NewStyle().Underline(true).Foreground(lipgloss.Color("14"))
	stLinkOn = lipgloss.NewStyle().Reverse(true).Foreground(lipgloss.Color("14"))
	stBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	stStatus = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	stFlash  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

// rebuildDetail recomputes the detail content and the link list for the current selection.
func (m *tui) rebuildDetail() {
	m.links = nil
	m.detail.SetContent(m.detailContent())
	if m.linkSel >= len(m.links) {
		m.linkSel = -1
	}
}

// lnk renders a link, records it at the given rendered line, and highlights it when selected.
func (m *tui) lnk(kind, id, label string, line int) string {
	style := stLink
	if m.linkSel == len(m.links) {
		style = stLinkOn
	}
	m.links = append(m.links, uiLink{kind: kind, id: id, label: label, line: line})
	return style.Render(label)
}

// detailContent builds the whole pane: metadata, the markdown body through glamour, and the
// relations BY TYPE — blocked by (this task's dependencies, with live status), blocks (the reverse
// edge), docs, commits — every entry a followable link.
func (m *tui) detailContent() string {
	if m.mode == modeDocs {
		return m.docDetailContent()
	}
	t, ok := m.selectedTask()
	if !ok {
		return stDim.Render("(nothing selected)")
	}
	var b strings.Builder
	line := 0
	writeln := func(s string) {
		b.WriteString(s + "\n")
		line += strings.Count(s, "\n") + 1
	}

	head := stTitle.Render(t.ID) + "  [" + string(t.Status) + "]  " + t.Priority
	for _, tag := range t.Tags {
		head += "  " + m.lnk("tag", tag, "#"+tag, line)
	}
	writeln(head)
	writeln("")
	writeln(stDim.Render("epic  ") + t.Epic)
	// slug and touchpoints are deliberately not shown: they are metadata for `todo path` and doc
	// linking, not something a person reads down the list; the CLI still carries them.
	if len(t.DepText) > 0 {
		writeln(stDim.Render("dep   ") + t.DepText)
	}
	if len(t.DeletedAt) > 0 {
		writeln(stFlash.Render("deleted " + t.DeletedAt))
	}
	writeln("")
	writeln(m.markdown(t.Text))

	if len(t.DependsOn) > 0 {
		writeln(stSect.Render("blocked by"))
		for _, depID := range t.DependsOn {
			status := "?"
			if dep, ok, _ := m.store.Get(depID); ok {
				status = string(dep.Status)
			}
			writeln("  " + m.lnk("task", depID, depID, line) + stDim.Render("  "+status))
		}
		writeln("")
	}
	if blocked, _ := m.store.Impact(t.ID); len(blocked) > 0 {
		writeln(stSect.Render("blocks"))
		for _, bt := range blocked {
			writeln("  " + m.lnk("task", bt.ID, bt.ID, line) + "  " + stDim.Render(oneLine(bt.Text, 40)))
		}
		writeln("")
	}
	if docs, _ := m.store.DocsOf(t.ID); len(docs) > 0 {
		writeln(stSect.Render("docs"))
		for _, d := range docs {
			writeln("  " + m.lnk("doc", d.ID, d.ID, line) + "  " + stDim.Render(oneLine(d.Title, 40)))
		}
		writeln("")
	}
	if commits, _ := m.store.CommitsOf(t.ID); len(commits) > 0 {
		writeln(stSect.Render("commits"))
		for _, c := range commits {
			writeln("  " + c.Ref + "  " + stDim.Render(whenShort(c.At)+"  "+oneLine(c.Note, 40)))
		}
	}
	return b.String()
}

func (m *tui) docDetailContent() string {
	d, ok := m.selectedDoc()
	if !ok {
		return stDim.Render("(nothing selected)")
	}
	var b strings.Builder
	line := 0
	writeln := func(s string) {
		b.WriteString(s + "\n")
		line += strings.Count(s, "\n") + 1
	}
	writeln(stTitle.Render(d.ID) + "  [" + d.Kind + "]  " + d.Path)
	writeln("")
	writeln(m.markdown(d.Body))
	// The section is structure, not an edge: a README lists the pages beside it, a page points back
	// at its README, and both come from the path alone.
	if sec, page := todo.SplitSection(d.Path); len(sec) > 0 {
		if pages, _ := m.store.SectionDocs(sec); len(pages) > 1 {
			if page == "README" {
				writeln(stSect.Render("section " + sec))
				for _, p := range pages {
					if p.ID == d.ID {
						continue
					}
					writeln("  " + m.lnk("doc", p.ID, p.ID, line) + "  " + stDim.Render(oneLine(p.Title, 40)))
				}
				writeln("")
			} else if pages[0].ID != d.ID && strings.HasSuffix(pages[0].Path, "/README") {
				writeln(stSect.Render("section"))
				writeln("  " + m.lnk("doc", pages[0].ID, pages[0].ID, line) + "  " + stDim.Render(oneLine(pages[0].Title, 40)))
				writeln("")
			}
		}
	}
	if rel, _ := m.store.RelatedDocs(d.ID); len(rel) > 0 {
		writeln(stSect.Render("docs"))
		for _, r := range rel {
			writeln("  " + m.lnk("doc", r.ID, r.ID, line) + "  " + stDim.Render(oneLine(r.Title, 40)))
		}
		writeln("")
	}
	if tasks, _ := m.store.TasksOf(d.ID); len(tasks) > 0 {
		writeln(stSect.Render("tasks"))
		for _, t := range tasks {
			writeln("  " + m.lnk("task", t.ID, t.ID, line) + "  " + stDim.Render(oneLine(t.Text, 40)))
		}
	}
	return b.String()
}

// markdown renders through glamour — real terminal markdown with code highlighting — and falls
// back to the raw text if the renderer is missing or fails.
func (m *tui) markdown(s string) string {
	if m.mdRender == nil {
		return s
	}
	out, err := m.mdRender.Render(s)
	if err != nil {
		return s
	}
	return strings.TrimRight(out, "\n")
}

func (m *tui) View() string {
	if m.width == 0 {
		return "loading…"
	}
	switch m.focus {
	case focusForm:
		if m.form != nil {
			return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
				stBorder.Render(m.form.View()))
		}
	case focusViewer:
		return stBorder.Width(m.width-2).Render(m.viewer.View()) + "\n" +
			stStatus.Render(" VIEW  ↑↓ scroll · Esc/q/v back")
	}

	list := m.table.View()
	detailStyle := stBorder
	if m.focus == focusDetail {
		detailStyle = detailStyle.BorderForeground(lipgloss.Color("10"))
	}
	detail := detailStyle.Width(m.paneWidth()).Height(m.paneHeight()).Render(m.detail.View())
	body := lipgloss.JoinHorizontal(lipgloss.Top, list, " ", detail)

	status := m.statusLine()
	return body + "\n" + status
}

func (m *tui) statusLine() string {
	if m.focus == focusSearch {
		return " / " + m.searchIn.View()
	}
	if len(m.flash) > 0 {
		return stFlash.Render(" " + m.flash)
	}
	if m.focus == focusDetail {
		return stStatus.Render(" DETAIL  ↑↓ scroll · n/p links · Enter follow · Tab/Esc back")
	}
	scope := "live"
	if m.trash {
		scope = "TRASH"
	}
	if m.mode == modeDocs {
		scope = "WIKI"
	}
	tag := strings.Join(m.tags, "+")
	if len(tag) == 0 {
		tag = "all"
	}
	stf := string(m.statusF)
	if len(stf) == 0 {
		stf = "any"
	}
	srch := ""
	if len(m.search) > 0 {
		srch = "  search:" + m.search
	}
	n := len(m.tasks)
	if m.mode == modeDocs {
		n = len(m.docs)
	}
	return stStatus.Render(fmt.Sprintf(
		" %s  tag:%s  status:%s%s  |  %d shown  |  a add · e edit · d del · c commit · l link · t trash · s status · f tag · 1-4 sort · Tab detail · / search · v view · w wiki · q quit",
		scope, tag, stf, srch, n))
}
