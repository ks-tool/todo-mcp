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
	"os/exec"
	"path/filepath"
	"strconv"
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
		Long: `Run with no command to open the interactive TUI (backlog + wiki, soft-delete, trash).
The database, for every command: --db <path>, else TODO_DB (read from ./.mcp.json before the ambient
value), else ./backlog.db, else the XDG default.
Output is a table at a terminal, JSON Lines into a pipe or under --json. Exit: 0 found, 2 empty, 1 error.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// No subcommand opens the TUI — the interactive face is the default one a person reaches for.
		Args: cobra.NoArgs,
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, _ []string) error { return runTUI(st) }),
	}
	root.PersistentFlags().StringVar(&flagDB, "db", "", "database file (overrides $TODO_DB and discovery)")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "JSON Lines output even at a terminal")

	root.AddCommand(taskCommands()...)
	root.AddCommand(wikiCommands()...)
	root.AddCommand(commentCommands()...)
	root.AddCommand(trailerCommands()...)
	root.AddCommand(pathCommand())
	root.AddCommand(explainCommand())
	root.AddCommand(contractCommand())
	root.AddCommand(frontCommands()...)
	return root
}

// contractCommand checks the API contract between two services from their OpenAPI specs — the fast,
// language-agnostic answer to "did we break the contract".
func contractCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "contract <consumer-spec> <provider-spec>",
		Short: "check an API contract between two OpenAPI (JSON) specs",
		Long: `Compare what a CONSUMER expects (the provider spec it was built against) against what the
PROVIDER now offers, and report contract breaks: an endpoint the consumer needs that the provider
dropped (orphan-call), or one whose request/response shape diverged (schema-drift). Exit 0 when the
contract holds, 2 when any break is found.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := todo.CheckContractFiles(args[0], args[1])
			if err != nil {
				return err
			}
			if jsonOut() {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(c); err != nil {
					return err
				}
			} else {
				printContract(c)
			}
			if len(c.Breaks) > 0 {
				os.Exit(2)
			}
			return nil
		},
	}
}

func printContract(c *todo.Contract) {
	fmt.Printf("matched %d endpoint(s)\n", len(c.Matched))
	for _, e := range c.Matched {
		fmt.Printf("  ok  %s %s\n", e.Method, e.Path)
	}
	if len(c.Breaks) == 0 {
		fmt.Fprintln(os.Stderr, "contract intact")
		return
	}
	fmt.Printf("\nBREAKS (%d):\n", len(c.Breaks))
	for _, b := range c.Breaks {
		fmt.Printf("  [%s] %s %s — %s\n", b.Kind, b.Method, b.Path, b.Detail)
	}
}

// explainCommand is the graphify `explain` view over the ingested symbol graph: a node, where it
// lives, how connected it is, and its edges with their relation and inferred/extracted confidence.
func explainCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "explain <node>",
		Short: "a code symbol: its source and its connections (via the ingested graphify graph)",
		Args:  cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			ex, ok, err := st.Explain(mustFlag(cmd, "repo"), args[0])
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(os.Stderr, "no such node")
				os.Exit(2)
			}
			if jsonOut() {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(ex)
			}
			printExplain(ex)
			return nil
		}),
	}
	c.Flags().String("repo", "", "restrict to one repo")
	return c
}

func printExplain(ex *todo.SymbolExplain) {
	fmt.Printf("Node: %s\n", ex.Symbol.Label)
	fmt.Printf("  Source:    %s %s\n", ex.Symbol.File, ex.Symbol.Line)
	fmt.Printf("  Repo:      %s\n", ex.Symbol.Repo)
	fmt.Printf("  Degree:    %d\n\n", ex.Degree)
	fmt.Printf("Connections (%d):\n", len(ex.Conns))
	for _, c := range ex.Conns {
		arrow := "-->"
		if c.Dir == "in" {
			arrow = "<--"
		}
		fmt.Printf("  %s %-32s [%s] [%s]\n", arrow, c.Label, c.Relation, c.Confidence)
	}
}

// commentCommands are a task's comment thread: many timestamped entries, no author, soft-deleted
// like everything else. Separate from `note`, which sets the single done_note annotation.
func commentCommands() []*cobra.Command {
	comment := &cobra.Command{Use: "comment", Short: "a task's comment thread (timestamped, no author)"}

	add := &cobra.Command{
		Use: "add <task-id> [text...]", Short: "append a comment to a task's thread", Args: cobra.MinimumNArgs(1),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			text := mustFlag(cmd, "text")
			if len(text) == 0 && len(args) > 1 {
				text = strings.Join(args[1:], " ")
			}
			if len(strings.TrimSpace(text)) == 0 {
				return fmt.Errorf("a comment needs text")
			}
			id, err := st.AddComment(args[0], text, time.Now().Format(time.RFC3339))
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "added comment %d to %s\n", id, args[0])
			fmt.Println(id)
			return nil
		}),
	}
	add.Flags().String("text", "", "the comment (or pass it as trailing words)")

	list := &cobra.Command{
		Use: "list <task-id>", Short: "the task's thread, oldest first", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			cs, err := st.Comments(args[0])
			if err != nil {
				return err
			}
			if jsonOut() {
				enc := json.NewEncoder(os.Stdout)
				for _, c := range cs {
					_ = enc.Encode(c)
				}
				if len(cs) == 0 {
					os.Exit(2)
				}
				return nil
			}
			if len(cs) == 0 {
				fmt.Fprintln(os.Stderr, "(none)")
				os.Exit(2)
			}
			for _, c := range cs {
				fmt.Printf("%-6d  %-16s  %s\n", c.ID, whenShort(c.At), oneLine(c.Text, 80))
			}
			return nil
		}),
	}

	edit := &cobra.Command{
		Use: "edit <comment-id> [text...]", Short: "rewrite a comment", Args: cobra.MinimumNArgs(1),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("comment id must be a number: %s", args[0])
			}
			text := mustFlag(cmd, "text")
			if len(text) == 0 && len(args) > 1 {
				text = strings.Join(args[1:], " ")
			}
			ok, err := st.EditComment(id, text)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(os.Stderr, "no such comment: %d\n", id)
				os.Exit(2)
			}
			fmt.Fprintf(os.Stderr, "comment %d updated\n", id)
			return nil
		}),
	}
	edit.Flags().String("text", "", "the new comment (or pass it as trailing words)")

	rm := &cobra.Command{
		Use: "rm <comment-id>", Short: "soft-delete a comment", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("comment id must be a number: %s", args[0])
			}
			ok, err := st.DeleteComment(id, time.Now().Format(time.RFC3339))
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(os.Stderr, "no such comment: %d\n", id)
				os.Exit(2)
			}
			fmt.Fprintf(os.Stderr, "comment %d deleted\n", id)
			return nil
		}),
	}

	comment.AddCommand(add, list, edit, rm)
	return []*cobra.Command{comment}
}

// pathCommand walks the graph: the shortest chain of edges between two nodes, which is how a person
// asks "how do these relate" — an intent to the commits behind it, a commit to its ancestry.
func pathCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "path <A> <B>",
		Short: "shortest chain of edges between two nodes (task/commit/doc)",
		Long: `Resolve A and B — each a task id, a commit sha (full or a 7+ char prefix), a doc id, or a
full-text phrase resolved to the node that mentions it most — and print the shortest path between
them: a task to the commits that closed it (commit), a commit to its parents (parent), a task to its
dependencies (dep), a task or a doc to the pages it links (doc). --epic and --tag scope the nodes.`,
		Args: cobra.ExactArgs(2),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			scope := todo.PathScope{Epic: mustFlag(cmd, "epic"), Tags: splitComma(mustFlag(cmd, "tag"))}
			p, ok, err := st.Path(args[0], args[1], scope)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(os.Stderr, "no path")
				os.Exit(2)
			}
			if jsonOut() {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(p)
			}
			printPath(p)
			return nil
		}),
	}
	c.Flags().String("epic", "", "restrict the path to one epic")
	c.Flags().String("tag", "", "restrict the path to these tags (comma = AND)")
	return c
}

func printPath(p *todo.Path) {
	fmt.Println(pathNodeLine(p.Start))
	for _, s := range p.Steps {
		fmt.Printf("  ─%s→ %s\n", s.Edge, pathNodeLine(s.Node))
	}
}

func pathNodeLine(n todo.PathNode) string {
	h := n.ID
	if n.Kind == todo.KindTrailer {
		h = shortref(n.ID)
	}
	return fmt.Sprintf("%-8s %-26s %s", n.Kind, h, oneLine(n.Label, 60))
}

// trailerCommands operate on the derived layer's nodes — the git commits reindex projects into the
// graph. Only the epic binding is authored (local, kept across a reindex); listing reads the cache.
func trailerCommands() []*cobra.Command {
	trailer := &cobra.Command{Use: "trailer", Short: "git commits projected into the graph (reindex fills them)"}

	bind := &cobra.Command{
		Use: "bind <sha> <epic>", Short: "file a trailer under a local epic (--del to clear)", Args: cobra.RangeArgs(1, 2),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			if mustBool(cmd, "del") {
				if err := st.UnbindTrailerEpic(args[0]); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "%s unbound\n", shortref(args[0]))
				return nil
			}
			if len(args) != 2 {
				return fmt.Errorf("bind needs <sha> <epic> (or <sha> --del)")
			}
			if err := st.BindTrailerEpic(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "%s -> %s\n", shortref(args[0]), args[1])
			return nil
		}),
	}
	bind.Flags().Bool("del", false, "clear the binding instead of setting it")

	epic := &cobra.Command{
		Use: "epic <sha>", Short: "the epic a trailer resolves to (binding, else inherited, else repo)", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			e, err := st.TrailerEpic(args[0])
			if err != nil {
				return err
			}
			if len(e) == 0 {
				fmt.Fprintln(os.Stderr, "(no epic)")
				os.Exit(2)
			}
			fmt.Println(e)
			return nil
		}),
	}

	list := &cobra.Command{
		Use: "list [repo]", Short: "the trailer nodes in the cache, optionally by repo and --tag", Args: cobra.MaximumNArgs(1),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			repo := ""
			if len(args) > 0 {
				repo = args[0]
			}
			ts, err := st.TrailersFiltered(repo, splitComma(mustFlag(cmd, "tag")))
			if err != nil {
				return err
			}
			if jsonOut() {
				enc := json.NewEncoder(os.Stdout)
				for _, t := range ts {
					_ = enc.Encode(t)
				}
				if len(ts) == 0 {
					os.Exit(2)
				}
				return nil
			}
			if len(ts) == 0 {
				fmt.Fprintln(os.Stderr, "(none)")
				os.Exit(2)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "SHA\tREPO\tWHEN\tSUBJECT")
			for _, t := range ts {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", shortref(t.SHA), trunc(t.Repo, 16), whenShort(t.At), oneLine(t.Subject, 60))
			}
			return w.Flush()
		}),
	}

	list.Flags().String("tag", "", "only trailers carrying these tags (comma = AND); tags come from commit messages")
	trailer.AddCommand(bind, epic, list)
	return []*cobra.Command{trailer}
}

// shortref is the seven-character handle a person reads a commit by, for CLI output; the store keeps
// the full sha.
func shortref(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// commitKnown reports whether a sha is one this backlog can vouch for: a commit in the git repo at
// dir, OR a trailer already in the cache (reindex loaded it from some repo's history). The trailer
// check is what makes the guard work across a multi-project backlog — a commit of ANOTHER project,
// reindexed into the same database, is known even though the current directory's repo does not hold
// it — while a dependency's sha, in no tracked history, stays unknown.
func commitKnown(st *todo.Store, dir, sha string) bool {
	if ok, _ := st.HasTrailer(sha); ok {
		return true
	}
	_, has := todo.RepoHasCommit(dir, sha)
	return has
}

// resolveDB is where the backlog comes from, in one fixed order: --db, then TODO_DB — read the same
// way the MCP host reads it, from the .mcp.json in the current directory before the ambient
// environment (configEnv) — then a backlog.db in the current directory (the project-local
// convention), and the XDG default so a plain `todo` still has a home.
func resolveDB() string {
	if len(flagDB) > 0 {
		return flagDB
	}
	if p := configEnv("TODO_DB"); len(p) > 0 {
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

// configEnv reads a project setting the way the MCP host would — from the `todo` server's `env` in
// the .mcp.json of the CURRENT directory (no walk-up; the project root is where the file and the
// commands both live), falling back to the ambient environment when the file, the entry or the key
// is absent. It is what lets `todo` on a shell reach the very database and epics the server serves,
// so a `reindex` or an `add` from the project root needs no --db.
func configEnv(key string) string {
	if v, ok := projectEnv()[key]; ok {
		return v
	}
	return os.Getenv(key)
}

// projectEnv is the `todo` server's env block from ./.mcp.json, or nil when there is none to read.
func projectEnv() map[string]string {
	cfg, err := readMCPConfig(".mcp.json")
	if err != nil {
		return nil
	}
	entry, ok := cfg.Servers["todo"].(map[string]any)
	if !ok {
		return nil
	}
	env, ok := entry["env"].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
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
			epic := firstNonEmpty(mustFlag(cmd, "epic"), rootEpic())
			if len(strings.TrimSpace(epic)) == 0 || len(strings.TrimSpace(text)) == 0 {
				return fmt.Errorf("add needs an epic (--epic, or the first of the TODO_EPICS install writes) and the text")
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
			for _, k := range []string{"priority", "epic", "slug", "text", "dep", "tags", "touch", "status"} {
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
	for _, k := range []string{"priority", "epic", "slug", "text", "dep", "tags", "touch", "status"} {
		edit.Flags().String(k, "", "new "+k)
	}

	note := &cobra.Command{
		Use: "note <id> [text...]", Short: "set or edit a task's comment — works on a done task, no reopen", Args: cobra.MinimumNArgs(1),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			text := mustFlag(cmd, "text")
			if len(text) == 0 && len(args) > 1 {
				text = strings.Join(args[1:], " ")
			}
			ok, err := st.SetNote(args[0], text)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(os.Stderr, "no such task: %s\n", args[0])
				os.Exit(2)
			}
			fmt.Fprintf(os.Stderr, "%s note set\n", args[0])
			return nil
		}),
	}
	note.Flags().String("text", "", "the comment (or pass it as trailing words); empty clears it")

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
		add, edit, note, dep, suggest, del, restore, trash, render, imp,
	}
}

// frontCommands are the other faces over the same store, plus the environment plumbing. The TUI is
// not among them: it is the root command's own default, reached by running `todo` with no subcommand.
func frontCommands() []*cobra.Command {
	mcp := &cobra.Command{
		Use: "mcp", Short: "serve the backlog as MCP tools over stdio", Args: cobra.NoArgs,
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, _ []string) error { return runMCP(st) }),
	}
	install := &cobra.Command{
		Use: "install", Short: "wire the MCP server into the project's .mcp.json and CLAUDE.md", Args: cobra.NoArgs,
		// The epics name which slices of the shared database ARE this project; the first is the
		// root. Unnamed, the list defaults to the project directory's own name, which is what a
		// person would have typed.
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := mustFlag(cmd, "dir")
			epics := splitComma(mustFlag(cmd, "epics"))
			if len(epics) == 0 {
				abs, err := filepath.Abs(dir)
				if err != nil {
					return err
				}
				epics = []string{filepath.Base(abs)}
			}
			return runInstall(dir, flagDB, epics, mustFlag(cmd, "instructions"))
		},
	}
	install.Flags().String("dir", ".", "project directory")
	install.Flags().String("epics", "", "the project's epics, comma-separated; the first is the root (default: the directory's name)")
	install.Flags().String("instructions", instructionsDefault, "file for the agent usage block (e.g. AGENTS.md); 'none' writes no block")
	uninstall := &cobra.Command{
		Use: "uninstall", Short: "remove the todo entry and the instructions block", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUninstall(mustFlag(cmd, "dir"), mustFlag(cmd, "instructions"))
		},
	}
	uninstall.Flags().String("dir", ".", "project directory")
	uninstall.Flags().String("instructions", instructionsDefault, "file the usage block was installed into")
	reindex := &cobra.Command{
		Use:   "reindex",
		Short: "rebuild the derived trailer layer from git (main, by convention)",
		Long: `Read the whole log of --rev (default 'main') in --dir and rebuild the trailer cache from it —
one node per commit, parents as edges. The authored tasks and trailer→epic bindings are untouched.
Meant to be cheap and repeatable: a git post-merge/post-checkout hook, the start of ` + "`todo mcp`" + `, or by
hand after a fetch. --repo labels the source (default: the directory's name).`,
		Args: cobra.NoArgs,
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, _ []string) error {
			dir := mustFlag(cmd, "dir")
			n, err := st.Reindex(dir, reindexRepo(mustFlag(cmd, "repo"), dir), mustFlag(cmd, "rev"))
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "reindexed %d commits\n", n)
			return nil
		}),
	}
	reindex.Flags().String("dir", ".", "the git repository")
	reindex.Flags().String("rev", "main", "the ref or range to read")
	reindex.Flags().String("repo", "", "source label for the trailers (default: the directory's name)")

	symbols := &cobra.Command{
		Use:   "symbols <dir>",
		Short: "extract code symbols with graphify and ingest them (per repo)",
		Long: `Run graphify's extractor on the repo — or ingest a prebuilt graph.json with --graph — and load
its symbol nodes into the backlog, scoped by repo, so code symbols join the graph the tasks and
commits live in. Needs graphify on PATH unless --graph is given. --repo labels the source (default:
the directory's name).`,
		Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			dir := args[0]
			graph := mustFlag(cmd, "graph")
			if len(graph) == 0 {
				if err := exec.Command("graphify", "update", dir, "--no-cluster").Run(); err != nil {
					return fmt.Errorf("run graphify update (or pass --graph <graph.json>): %w", err)
				}
				graph = filepath.Join(dir, "graphify-out", "graph.json")
			}
			n, err := st.IngestGraph(reindexRepo(mustFlag(cmd, "repo"), dir), graph)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "ingested %d symbols\n", n)
			return nil
		}),
	}
	symbols.Flags().String("graph", "", "prebuilt graphify graph.json to ingest (skips running graphify)")
	symbols.Flags().String("repo", "", "source label for the symbols (default: the directory's name)")

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
	return []*cobra.Command{mcp, install, uninstall, reindex, symbols, schema, backup}
}

// reindexRepo is the source label for a reindex: the given --repo, or the directory's own name when
// none is given — a stable handle for the commits' origin, which a trailer resolves its epic through
// when nothing local overrides it.
func reindexRepo(repo, dir string) string {
	if len(repo) > 0 {
		return repo
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return filepath.Base(abs)
	}
	return dir
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
			"add":     "[--epic <e>] [--tags --priority --slug --touch --dep] <text> — epic defaults to the first of $TODO_EPICS",
			"edit":    "<id> [--priority --epic --slug --text --dep --status]",
			"note":    "<id> [text] — set/edit the single done_note annotation; no reopen",
			"comment": "add|list|edit|rm — a task's comment thread (timestamped, no author)",
			"dep":     "<id> <depends-on-id> [--del]", "suggest": "<id> [--apply]",
			"delete": "<id> — soft-delete to trash", "restore": "<id>", "trash": "the soft-deleted",
			"render": "[tag] — rebuild markdown to stdout",
			"backup": "[dir-or-file] — verified VACUUM INTO snapshot; never overwrites",
			"doc":    "add|show|edit|list|rm|restore|import|link-slugs — the wiki",
			"docs":   "<task-or-doc-id> — the docs a task maps to; a doc's related pages", "tasks": "<doc-id> — the tasks a doc maps to",
			"commit": "<task> <sha> [--del] — record/unlink a commit; a foreign sha is refused", "commits": "<task>",
			"sync-commits": "[--dir --rev] — scan git log for task ids",
			"reindex":      "[--dir --rev --repo] — rebuild the derived trailer layer from git",
			"symbols":      "<dir> [--graph --repo] — extract code symbols via graphify and ingest them",
			"path":         "<A> <B> [--epic --tag] — shortest chain of edges between two nodes",
			"explain":      "<node> [--repo] — a code symbol's source, degree and connections",
			"contract":     "<consumer-spec> <provider-spec> — check an API contract between two OpenAPI specs",
			"trailer":      "bind|epic|list — the git commits reindex projects into the graph",
		},
		"output": "JSON Lines under --json or into a pipe; exit 0 found / 2 empty / 1 error",
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(schema)
}
