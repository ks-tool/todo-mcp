package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ks-tool/todo-mcp/internal/todo"
)

// wikiCommands are the wiki and the mappings: doc CRUD, the two directions of the task↔doc map,
// and the commit records.
func wikiCommands() []*cobra.Command {
	doc := &cobra.Command{Use: "doc", Short: "the wiki: markdown pages tasks map onto"}

	docAdd := &cobra.Command{
		Use: "add [body...]", Short: "create or replace a page (the id derives from --path)",
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			path := mustFlag(cmd, "path")
			body := mustFlag(cmd, "body")
			if len(body) == 0 {
				body = strings.Join(args, " ")
			}
			if len(path) == 0 || len(body) == 0 {
				return fmt.Errorf("doc add needs --path and a body (--body or trailing words)")
			}
			id := mustFlag(cmd, "id")
			if len(id) == 0 {
				id = todo.DocID(path)
			}
			d := todo.Doc{ID: id, Path: path,
				Title: firstNonEmpty(mustFlag(cmd, "title"), path),
				Kind:  firstNonEmpty(mustFlag(cmd, "kind"), "note"), Body: body}
			if err := st.PutDoc(d); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "added", id)
			fmt.Println(id)
			return nil
		}),
	}
	docAdd.Flags().String("path", "", "the slug a task's slug field matches (required)")
	docAdd.Flags().String("title", "", "page title (default: the path)")
	docAdd.Flags().String("kind", "note", "design|threat-model|note|adr|reference")
	docAdd.Flags().String("body", "", "markdown body (or pass it as trailing words)")
	docAdd.Flags().String("id", "", "explicit id (default: derived from the path)")

	docShow := &cobra.Command{
		Use: "show <id>", Short: "one page in full", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			d, ok, err := st.GetDoc(args[0])
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(os.Stderr, "no such doc: %s\n", args[0])
				os.Exit(2)
			}
			if jsonOut() {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(d)
			}
			fmt.Printf("%s  [%s]  %s\n%s\n\n%s\n", d.ID, d.Kind, d.Path, d.Title, d.Body)
			return nil
		}),
	}

	docEdit := &cobra.Command{
		Use: "edit <id>", Short: "change named fields of a page", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			cur, ok, err := st.GetDoc(args[0])
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(os.Stderr, "no such doc: %s\n", args[0])
				os.Exit(2)
			}
			for _, f := range []struct {
				name string
				dst  *string
			}{{"path", &cur.Path}, {"title", &cur.Title}, {"kind", &cur.Kind}, {"body", &cur.Body}} {
				if cmd.Flags().Changed(f.name) {
					*f.dst = mustFlag(cmd, f.name)
				}
			}
			if err := st.PutDoc(cur); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, args[0], "updated")
			return nil
		}),
	}
	for _, k := range []string{"path", "title", "kind", "body"} {
		docEdit.Flags().String(k, "", "new "+k)
	}

	docList := &cobra.Command{
		Use: "list", Short: "pages, under a full-text search or one section", Args: cobra.NoArgs,
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, _ []string) error {
			var ds []todo.Doc
			var err error
			if sec := mustFlag(cmd, "section"); len(sec) > 0 {
				ds, err = st.SectionDocs(sec)
			} else {
				ds, err = st.ListDocs(mustFlag(cmd, "search"))
			}
			if err != nil {
				return err
			}
			emitDocs(ds)
			return nil
		}),
	}
	docList.Flags().String("search", "", "full-text over title and body")
	docList.Flags().String("section", "", "one section's pages (the path prefix), README first")

	docRm := &cobra.Command{
		Use: "rm <id>", Short: "soft-delete a page", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			ok, err := st.DeleteDoc(args[0], time.Now().Format(time.RFC3339))
			if err != nil {
				return err
			}
			if !ok {
				os.Exit(2)
			}
			fmt.Fprintf(os.Stderr, "%s moved to trash\n", args[0])
			return nil
		}),
	}

	docRestore := &cobra.Command{
		Use: "restore <id>", Short: "restore a soft-deleted page", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			ok, err := st.RestoreDoc(args[0])
			if err != nil {
				return err
			}
			if !ok {
				os.Exit(2)
			}
			fmt.Fprintf(os.Stderr, "%s restored\n", args[0])
			return nil
		}),
	}

	docImport := &cobra.Command{
		Use: "import <file.md>...", Short: "bring markdown files in as pages (path = base name)", Args: cobra.MinimumNArgs(1),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			for _, f := range args {
				d, err := todo.ImportDoc(f, mustFlag(cmd, "kind"))
				if err != nil {
					return err
				}
				if err := st.PutDoc(d); err != nil {
					return err
				}
			}
			fmt.Fprintf(os.Stderr, "imported %d docs\n", len(args))
			return nil
		}),
	}
	docImport.Flags().String("kind", "reference", "kind for the imported pages")

	docLinkSlugs := &cobra.Command{
		Use: "link-slugs", Short: "link every task to the doc whose path matches its slug", Args: cobra.NoArgs,
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, _ []string) error {
			n, err := st.LinkDocsBySlug()
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "linked %d task→doc edges by matching slug to path\n", n)
			return nil
		}),
	}
	doc.AddCommand(docAdd, docShow, docEdit, docList, docRm, docRestore, docImport, docLinkSlugs)

	docs := &cobra.Command{
		Use: "docs <task-or-doc-id>", Short: "the docs a task maps to; for a doc id, its related pages", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			// A doc id answers with its neighbourhood — linked either way — because that is the
			// question a doc id asks; a task's edges only ever point outward.
			if _, ok, _ := st.GetDoc(args[0]); ok {
				ds, err := st.RelatedDocs(args[0])
				if err != nil {
					return err
				}
				emitDocs(ds)
				return nil
			}
			ds, err := st.DocsOf(args[0])
			if err != nil {
				return err
			}
			emitDocs(ds)
			return nil
		}),
	}

	tasks := &cobra.Command{
		Use: "tasks <doc-id>", Short: "the tasks a doc maps to (doc -> task)", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			ts, err := st.TasksOf(args[0])
			if err != nil {
				return err
			}
			emit(ts)
			return nil
		}),
	}

	link := &cobra.Command{
		Use: "link <task-or-doc-id> <doc|commit|url> <ref>", Short: "map a task — or a doc — to a thing (--del unlinks)", Args: cobra.ExactArgs(3),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			if mustBool(cmd, "del") {
				return st.Unlink(args[0], args[1], args[2])
			}
			return st.Link(args[0], args[1], args[2], mustFlag(cmd, "note"))
		}),
	}
	link.Flags().Bool("del", false, "remove the edge")
	link.Flags().String("note", "", "note on the edge")

	commit := &cobra.Command{
		Use: "commit <task-id> <sha>", Short: "record (or --del unlink) a commit against a task", Args: cobra.ExactArgs(2),
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, args []string) error {
			if mustBool(cmd, "del") {
				return st.Unlink(args[0], todo.LinkCommit, args[1])
			}
			if err := guardRepoCommit(mustFlag(cmd, "dir"), args[1]); err != nil {
				return err
			}
			return st.Link(args[0], todo.LinkCommit, args[1], mustFlag(cmd, "note"), mustFlag(cmd, "at"))
		}),
	}
	commit.Flags().String("note", "", "the commit subject")
	commit.Flags().String("at", "", "commit date, ISO 8601 (e.g. from git show -s --format=%cI)")
	commit.Flags().String("dir", ".", "the repo to verify the sha against")
	commit.Flags().Bool("del", false, "unlink the commit instead of recording it")

	commits := &cobra.Command{
		Use: "commits <task-id>", Short: "the commits recorded against a task", Args: cobra.ExactArgs(1),
		RunE: withStore(func(st *todo.Store, _ *cobra.Command, args []string) error {
			links, err := st.CommitsOf(args[0])
			if err != nil {
				return err
			}
			if jsonOut() {
				enc := json.NewEncoder(os.Stdout)
				for _, l := range links {
					_ = enc.Encode(l)
				}
			} else {
				for _, l := range links {
					fmt.Printf("%-12s  %-16s  %s\n", l.Ref, whenShort(l.At), l.Note)
				}
			}
			if len(links) == 0 {
				os.Exit(2)
			}
			return nil
		}),
	}

	syncCommits := &cobra.Command{
		Use: "sync-commits", Short: "scan git log for task ids and record the mappings", Args: cobra.NoArgs,
		RunE: withStore(func(st *todo.Store, cmd *cobra.Command, _ []string) error {
			n, err := st.SyncCommits(mustFlag(cmd, "dir"), mustFlag(cmd, "rev"))
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "wrote %d commit links\n", n)
			return nil
		}),
	}
	syncCommits.Flags().String("dir", ".", "git repository")
	syncCommits.Flags().String("rev", "", "revision range, e.g. v1.0..HEAD")

	return []*cobra.Command{doc, docs, tasks, link, commit, commits, syncCommits}
}

// emitDocs prints a doc LIST — metadata only, in both shapes. The body belongs to `doc show`: a
// list is for choosing a page, and one page can be tens of kilobytes.
func emitDocs(docs []todo.Doc) {
	if jsonOut() {
		enc := json.NewEncoder(os.Stdout)
		for _, d := range docs {
			_ = enc.Encode(map[string]string{"id": d.ID, "path": d.Path, "title": d.Title, "kind": d.Kind})
		}
		if len(docs) == 0 {
			os.Exit(2)
		}
		return
	}
	if len(docs) == 0 {
		fmt.Fprintln(os.Stderr, "(none)")
		os.Exit(2)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tPATH\tTITLE")
	for _, d := range docs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", d.ID, d.Kind, d.Path, trunc(d.Title, 50))
	}
	_ = w.Flush()
}
