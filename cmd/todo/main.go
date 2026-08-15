// Command todo is a backlog you can ask questions of, from a terminal or from a program.
//
// One contract serves both readers. To a terminal it prints an aligned table; to a pipe, or under
// --json, it prints JSON Lines — one object per line, no colour, no spinner, nothing that has to be
// un-drawn to be parsed. Every read is one command answering one question, the exit code says
// whether there was an answer (0 found, 2 empty, 1 error), and `todo schema` hands a program the
// field and command contract so it need not guess.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ks-tool/todo-mcp/internal/todo"
)

// The two global flags. --db names the database outright; --json forces machine output at a tty.
var (
	flagDB   string
	flagJSON bool
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "todo:", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "todo",
		Short: "a backlog and a wiki you can query — CLI, TUI and MCP over one database",
		Long: `The database, for every command: --db <path>, else $TODO_DB, else ./backlog.db, else the XDG default.
Output is a table at a terminal, JSON Lines into a pipe or under --json. Exit: 0 found, 2 empty, 1 error.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flagDB, "db", "", "database file (overrides $TODO_DB and discovery)")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "JSON Lines output even at a terminal")

	root.AddCommand(taskCommands()...)
	root.AddCommand(wikiCommands()...)
	root.AddCommand(frontCommands()...)
	return root
}

// resolveDB is where the backlog comes from, in one fixed order: --db, the TODO_DB environment
// (what an MCP host passes the server from .mcp.json), a backlog.db in the current directory (the
// project-local convention), and the XDG default so a plain `todo` still has a home.
func resolveDB() string {
	if len(flagDB) > 0 {
		return flagDB
	}
	if p := os.Getenv("TODO_DB"); len(p) > 0 {
		return p
	}
	if _, err := os.Stat("backlog.db"); err == nil {
		return "backlog.db"
	}
	base := os.Getenv("XDG_DATA_HOME")
	if len(base) == 0 {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(base, "todo")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "backlog.db")
}

// withStore opens the database around a command body, so every RunE reads the same way and no
// command forgets to close.
func withStore(run func(st *todo.Store, cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		st, err := todo.Open(resolveDB())
		if err != nil {
			return err
		}
		defer func() { _ = st.Close() }()
		return run(st, cmd, args)
	}
}

func taskCommands() []*cobra.Command {
	list := &cobra.Command{
		Use:   "list",
		Short: "list tasks under filters",
		Args:  cobra.NoArgs,
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, _ []string) error {
			f := todo.Filter{Status: todo.StatusOpen}
			f.Tags = splitComma(mustFlag(cmd, "tag"))
			f.Epic = mustFlag(cmd, "epic")
			f.Priority = mustFlag(cmd, "priority")
			f.Search = mustFlag(cmd, "search")
			if v := mustFlag(cmd, "status"); len(v) > 0 {
				f.Status = todo.Status(v)
			}
			if mustBool(cmd, "all") {
				f.Status = ""
			}
			if mustBool(cmd, "ready") {
				r, err := st.Ready()
				if err != nil {
					return err
				}
				emit(filterInMemory(r, f))
				return nil
			}
			ts, err := st.List(f)
			if err != nil {
				return err
			}
			emit(ts)
			return nil
		}),
	}
	list.Flags().String("tag", "", "only tasks carrying these tags (comma = AND)")
	list.Flags().String("epic", "", "epic substring")
	list.Flags().String("priority", "", "exact priority, e.g. P2")
	list.Flags().String("search", "", "full-text query")
	list.Flags().String("status", "", "open|done (default open)")
	list.Flags().Bool("all", false, "both statuses")
	list.Flags().Bool("ready", false, "only tasks whose dependencies are done")

	ready := &cobra.Command{
		Use: "ready", Short: "open tasks whose every dependency is done", Args: cobra.NoArgs,
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, _ []string) error {
			ts, err := st.Ready()
			if err != nil {
				return err
			}
			emit(ts)
			return nil
		}),
	}

	next := &cobra.Command{
		Use: "next", Short: "the single most urgent ready task", Args: cobra.NoArgs,
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, _ []string) error {
			ts, err := st.Ready()
			if err != nil {
				return err
			}
			ts = filterInMemory(ts, todo.Filter{Tags: splitComma(mustFlag(cmd, "tag"))})
			if len(ts) > 1 {
				ts = ts[:1]
			}
			emit(ts)
			return nil
		}),
	}
	next.Flags().String("tag", "", "only tasks carrying these tags (comma = AND)")

	show := &cobra.Command{
		Use: "show <id>", Short: "one task in full", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			t, ok, err := st.Get(args[0])
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(os.Stderr, "no such task: %s\n", args[0])
				os.Exit(2)
			}
			if jsonOut() {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(t)
			}
			showHuman(t)
			return nil
		}),
	}

	impact := &cobra.Command{
		Use: "impact <id>", Short: "open tasks that depend on it", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			ts, err := st.Impact(args[0])
			if err != nil {
				return err
			}
			emit(ts)
			return nil
		}),
	}

	stats := &cobra.Command{
		Use: "stats", Short: "open and done counts per epic", Args: cobra.NoArgs,
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, _ []string) error {
			ss, err := st.Stats()
			if err != nil {
				return err
			}
			if jsonOut() {
				enc := json.NewEncoder(os.Stdout)
				for _, s := range ss {
					_ = enc.Encode(s)
				}
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "EPIC\tOPEN\tDONE")
			for _, s := range ss {
				fmt.Fprintf(w, "%s\t%d\t%d\n", s.Epic, s.Open, s.Done)
			}
			return w.Flush()
		}),
	}

	setStatus := func(use, short string, to todo.Status) *cobra.Command {
		return &cobra.Command{
			Use: use + " <id>", Short: short, Args: cobra.ExactArgs(1),
			RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
				ok, err := st.SetStatus(args[0], to)
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintf(os.Stderr, "no such task: %s\n", args[0])
					os.Exit(2)
				}
				fmt.Fprintf(os.Stderr, "%s -> %s\n", args[0], to)
				return nil
			}),
		}
	}

	add := &cobra.Command{
		Use: "add [text...]", Short: "create a task (the id is minted)",
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			text := mustFlag(cmd, "text")
			if len(text) == 0 {
				text = strings.Join(args, " ")
			}
			epic := firstNonEmpty(mustFlag(cmd, "epic"), os.Getenv("TODO_EPIC"))
			if len(strings.TrimSpace(epic)) == 0 || len(strings.TrimSpace(text)) == 0 {
				return fmt.Errorf("add needs an epic (--epic, or the TODO_EPIC install writes) and the text")
			}
			id, err := st.NextID(epic)
			if err != nil {
				return err
			}
			t := todo.Task{
				ID: id, Tags: splitComma(mustFlag(cmd, "tags")), Epic: epic, Status: todo.StatusOpen,
				Priority: mustFlag(cmd, "priority"), Slug: mustFlag(cmd, "slug"),
				DepText: mustFlag(cmd, "dep"), Text: text,
				Touch: splitComma(mustFlag(cmd, "touch")),
			}
			if err := st.Put(t); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "added", id)
			fmt.Println(id)
			return nil
		}),
	}
	add.Flags().String("tags", "", "comma-separated tags (optional, free-form)")
	add.Flags().String("epic", "", "epic (required; a project is an epic)")
	add.Flags().String("priority", "", "e.g. P2")
	add.Flags().String("slug", "", "design-doc reference")
	add.Flags().String("dep", "", "free-text dependency note")
	add.Flags().String("touch", "", "comma-separated touchpoints")
	add.Flags().String("text", "", "the task text (or pass it as trailing words)")

	edit := &cobra.Command{
		Use: "edit <id>", Short: "change named fields of a task", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			fields := map[string]string{}
			for _, k := range []string{"priority", "epic", "slug", "text", "dep", "tags", "status"} {
				if cmd.Flags().Changed(k) {
					fields[k] = mustFlag(cmd, k)
				}
			}
			if len(fields) == 0 {
				return fmt.Errorf("edit needs at least one field flag")
			}
			ok, err := st.Update(args[0], fields)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(os.Stderr, "no such task: %s\n", args[0])
				os.Exit(2)
			}
			fmt.Fprintf(os.Stderr, "%s updated\n", args[0])
			return nil
		}),
	}
	for _, k := range []string{"priority", "epic", "slug", "text", "dep", "tags", "status"} {
		edit.Flags().String(k, "", "new "+k)
	}

	dep := &cobra.Command{
		Use: "dep <id> <depends-on-id>", Short: "link (or --del unlink) a dependency edge", Args: cobra.ExactArgs(2),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			if mustBool(cmd, "del") {
				return st.DelDep(args[0], args[1])
			}
			return st.AddDep(args[0], args[1])
		}),
	}
	dep.Flags().Bool("del", false, "remove the edge")

	suggest := &cobra.Command{
		Use: "suggest <id>", Short: "dependency candidates from the dep: prose, via full-text", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			sg, err := st.Suggest(args[0])
			if err != nil {
				return err
			}
			if mustBool(cmd, "apply") {
				for _, x := range sg {
					if err := st.AddDep(x.TaskID, x.Candidate); err != nil {
						return err
					}
					fmt.Fprintf(os.Stderr, "linked %s -> %s\n", x.TaskID, x.Candidate)
				}
				return nil
			}
			if jsonOut() {
				enc := json.NewEncoder(os.Stdout)
				for _, x := range sg {
					_ = enc.Encode(x)
				}
			} else {
				for _, x := range sg {
					fmt.Printf("%-24s ← %q\n    %s\n", x.Candidate, x.Fragment, oneLine(x.CandText, 90))
				}
			}
			if len(sg) == 0 {
				os.Exit(2)
			}
			return nil
		}),
	}
	suggest.Flags().Bool("apply", false, "accept the top candidate for each fragment")

	del := &cobra.Command{
		Use: "delete <id>", Aliases: []string{"rm"}, Short: "soft-delete to the trash", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			ok, err := st.Delete(args[0], time.Now().Format(time.RFC3339))
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(os.Stderr, "no such live task: %s\n", args[0])
				os.Exit(2)
			}
			fmt.Fprintf(os.Stderr, "%s moved to trash (restore with: todo restore %s)\n", args[0], args[0])
			return nil
		}),
	}

	restore := &cobra.Command{
		Use: "restore <id>", Short: "restore from the trash", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			ok, err := st.Restore(args[0])
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(os.Stderr, "no such task: %s\n", args[0])
				os.Exit(2)
			}
			fmt.Fprintf(os.Stderr, "%s restored\n", args[0])
			return nil
		}),
	}

	trash := &cobra.Command{
		Use: "trash", Short: "the soft-deleted tasks", Args: cobra.NoArgs,
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, _ []string) error {
			ts, err := st.Trash()
			if err != nil {
				return err
			}
			emit(ts)
			return nil
		}),
	}

	render := &cobra.Command{
		Use: "render [tag]", Short: "rebuild the markdown from the db, to stdout", Args: cobra.MaximumNArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			f := todo.Filter{}
			if len(args) > 0 {
				f.Tags = []string{args[0]}
			}
			ts, err := st.List(f)
			if err != nil {
				return err
			}
			sortByOrder(ts)
			fmt.Print(todo.Render(ts))
			return nil
		}),
	}

	imp := &cobra.Command{
		Use: "import <tag|-> <file.md> [<tag|-> <file.md>...]", Short: "build the db from markdown checklists (- = no tag)",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) < 2 || len(args)%2 != 0 {
				return fmt.Errorf("import takes <tag|-> <file.md> pairs; - means no tag")
			}
			return nil
		},
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			total := 0
			for i := 0; i+1 < len(args); i += 2 {
				tag, path := strings.ToLower(args[i]), args[i+1]
				if tag == "-" {
					tag = ""
				}
				tasks, err := todo.Import(path, tag)
				if err != nil {
					return err
				}
				for _, t := range tasks {
					if err := st.Put(t); err != nil {
						return err
					}
				}
				fmt.Fprintf(os.Stderr, "%q: imported %d tasks from %s\n", tag, len(tasks), path)
				total += len(tasks)
			}
			fmt.Fprintf(os.Stderr, "total %d\n", total)
			return nil
		}),
	}

	return []*cobra.Command{
		list, ready, next, show, impact, stats,
		setStatus("done", "mark a task done", todo.StatusDone),
		setStatus("reopen", "reopen a done task", todo.StatusOpen),
		add, edit, dep, suggest, del, restore, trash, render, imp,
	}
}

// frontCommands are the other faces over the same store, plus the environment plumbing.
func frontCommands() []*cobra.Command {
	tui := &cobra.Command{
		Use: "tui", Short: "interactive terminal UI (backlog + wiki, soft-delete, trash)", Args: cobra.NoArgs,
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, _ []string) error { return runTUI(st) }),
	}
	mcp := &cobra.Command{
		Use: "mcp", Short: "serve the backlog as MCP tools over stdio", Args: cobra.NoArgs,
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, _ []string) error { return runMCP(st) }),
	}
	install := &cobra.Command{
		Use: "install", Short: "wire the MCP server into the project's .mcp.json and CLAUDE.md", Args: cobra.NoArgs,
		// The epic names which slice of the shared database IS this project; unnamed, it defaults
		// to the project directory's own name, which is what a person would have typed.
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := mustFlag(cmd, "dir")
			epic := mustFlag(cmd, "epic")
			if len(epic) == 0 {
				abs, err := filepath.Abs(dir)
				if err != nil {
					return err
				}
				epic = filepath.Base(abs)
			}
			return runInstall(dir, flagDB, epic, mustFlag(cmd, "instructions"))
		},
	}
	install.Flags().String("dir", ".", "project directory")
	install.Flags().String("epic", "", "the project's root epic (default: the directory's name)")
	install.Flags().String("instructions", instructionsDefault, "file for the agent usage block (e.g. AGENTS.md); 'none' writes no block")
	uninstall := &cobra.Command{
		Use: "uninstall", Short: "remove the todo entry and the instructions block", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUninstall(mustFlag(cmd, "dir"), mustFlag(cmd, "instructions"))
		},
	}
	uninstall.Flags().String("dir", ".", "project directory")
	uninstall.Flags().String("instructions", instructionsDefault, "file the usage block was installed into")
	schema := &cobra.Command{
		Use: "schema", Short: "the field and command contract, as JSON", Args: cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) { emitSchema() },
	}
	backup := &cobra.Command{
		Use:   "backup [dir-or-file]",
		Short: "write a verified snapshot of the database",
		Long: `A consistent snapshot via SQLite's own VACUUM INTO — safe while the database is in use, and
refused rather than overwritten when the destination exists. A directory (or no argument, meaning
the database's own directory) gets a timestamped file; an explicit file path is used as given.
The copy is then OPENED and counted, because a backup that cannot be read is not a backup.`,
		Args: cobra.MaximumNArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			src := resolveDB()
			dest := filepath.Dir(src)
			if len(args) > 0 {
				dest = args[0]
			}
			if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
				stamp := time.Now().Format("20060102-150405")
				base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
				dest = filepath.Join(dest, base+"-"+stamp+".db")
			}
			if err := st.BackupTo(dest); err != nil {
				return err
			}
			copyStore, err := todo.Open(dest)
			if err != nil {
				return fmt.Errorf("the snapshot was written but does not open: %w", err)
			}
			defer func() { _ = copyStore.Close() }()
			tasks, docs, err := copyStore.Counts()
			if err != nil {
				return fmt.Errorf("the snapshot was written but does not read: %w", err)
			}
			origTasks, origDocs, err := st.Counts()
			if err != nil {
				return err
			}
			if tasks != origTasks || docs != origDocs {
				return fmt.Errorf("the snapshot disagrees with the database: %d/%d tasks, %d/%d docs",
					tasks, origTasks, docs, origDocs)
			}
			fmt.Fprintf(os.Stderr, "backed up %d tasks and %d docs, verified by reading the copy\n", tasks, docs)
			fmt.Println(dest)
			return nil
		}),
	}
	return []*cobra.Command{tui, mcp, install, uninstall, schema, backup}
}

// jsonOut is true when the caller wants machine output: --json, or any time stdout is not a tty.
func jsonOut() bool {
	if flagJSON {
		return true
	}
	fi, _ := os.Stdout.Stat()
	return fi.Mode()&os.ModeCharDevice == 0
}

// mustFlag and mustBool read a flag that the command itself declared, so a miss is a programming
// error and not a user's.
func mustFlag(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}
func mustBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

func hasAllTags(t todo.Task, tags []string) bool {
	for _, want := range tags {
		want = strings.ToLower(want)
		found := false
		for _, x := range t.Tags {
			if x == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// filterInMemory narrows an already-computed set (used by --ready, which starts from the graph).
func filterInMemory(in []todo.Task, f todo.Filter) []todo.Task {
	var out []todo.Task
	for _, t := range in {
		if !hasAllTags(t, f.Tags) {
			continue
		}
		if len(f.Epic) > 0 && !strings.Contains(strings.ToLower(t.Epic), strings.ToLower(f.Epic)) {
			continue
		}
		if len(f.Priority) > 0 && t.Priority != f.Priority {
			continue
		}
		out = append(out, t)
	}
	return out
}

// emit is the shared output path for a list of tasks: JSON Lines for a program, a table for a
// person, and exit code 2 when the answer is empty so a caller can branch without parsing.
func emit(tasks []todo.Task) {
	if jsonOut() {
		enc := json.NewEncoder(os.Stdout)
		for _, t := range tasks {
			_ = enc.Encode(t)
		}
		if len(tasks) == 0 {
			os.Exit(2)
		}
		return
	}
	if len(tasks) == 0 {
		fmt.Fprintln(os.Stderr, "(none)")
		os.Exit(2)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPRI\tTAGS\tEPIC\tTASK")
	for _, t := range tasks {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Priority, strings.Join(t.Tags, ","), trunc(t.Epic, 22), oneLine(t.Text, 80))
	}
	_ = w.Flush()
}

func showHuman(t todo.Task) {
	fmt.Printf("%s  [%s]  %s  %s\n", t.ID, t.Status, t.Priority, strings.Join(t.Tags, " "))
	fmt.Printf("epic:  %s\n", t.Epic)
	if len(t.Slug) > 0 {
		fmt.Printf("doc:   %s\n", t.Slug)
	}
	if len(t.Touch) > 0 {
		fmt.Printf("files: %s\n", strings.Join(t.Touch, ", "))
	}
	if len(t.DepText) > 0 {
		fmt.Printf("dep:   %s\n", t.DepText)
	}
	if len(t.DoneSHA) > 0 {
		fmt.Printf("done:  %s\n", t.DoneSHA)
	}
	fmt.Printf("\n%s\n", t.Text)
}

func sortByOrder(ts []todo.Task) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j].Order < ts[j-1].Order; j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}

func emitSchema() {
	schema := map[string]any{
		"task_fields": map[string]string{
			"id": "stable identifier, e.g. scheduler-07 or ee-scheduler-07", "tags": "free labels, lower-cased; the filter takes a comma list a task must carry all of",
			"epic": "grouping heading; a project is an epic", "status": "open|done",
			"priority": "P0..P5 or a project's own labels", "rank": "priority as int, lower=more urgent",
			"slug": "design-doc reference", "touchpoints": "files the task touches",
			"dependsOn": "task ids this waits on", "depText": "raw dependency prose",
			"text": "the task itself", "doneSha": "commit that closed it", "doneNote": "the DONE annotation",
			"deletedAt": "RFC3339 when soft-deleted; empty means live",
		},
		"commands": map[string]string{
			"list":  "filter by --status/--tag/--epic/--priority/--search/--ready/--all",
			"ready": "open tasks with all deps done", "next": "the top ready task",
			"show": "<id> — one task", "impact": "<id> — tasks depending on it",
			"stats": "counts per epic", "done": "<id>", "reopen": "<id>",
			"add":  "[--epic <e>] [--tags --priority --slug --touch --dep] <text> — epic defaults to $TODO_EPIC",
			"edit": "<id> [--priority --epic --slug --text --dep --status]",
			"dep":  "<id> <depends-on-id> [--del]", "suggest": "<id> [--apply]",
			"delete": "<id> — soft-delete to trash", "restore": "<id>", "trash": "the soft-deleted",
			"render": "[tag] — rebuild markdown to stdout", "tui": "interactive terminal UI",
			"backup": "[dir-or-file] — verified VACUUM INTO snapshot; never overwrites",
			"doc":    "add|show|edit|list|rm|restore|import|link-slugs — the wiki",
			"docs":   "<task-or-doc-id> — the docs a task maps to; a doc's related pages", "tasks": "<doc-id> — the tasks a doc maps to",
			"commit": "<task> <sha> — record a commit", "commits": "<task>",
			"sync-commits": "[--dir --rev] — scan git log for task ids",
		},
		"output": "JSON Lines under --json or into a pipe; exit 0 found / 2 empty / 1 error",
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(schema)
}
