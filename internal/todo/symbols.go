package todo

import (
	"encoding/json"
	"os"
	"strings"
)

// The symbol layer ingests graphify's code graph. graphify does the extraction — parsing the source
// into a graph.json of nodes (files, functions, packages) and links (calls, imports, references)
// with an EXTRACTED/INFERRED confidence — and this file loads that JSON into the backlog, scoped by
// repo, so code nodes live in the same database as the tasks and commits and a path can cross
// between them. Rebuilding is per repo (like reindex): one project's symbols never wipe another's.

// graphFile is the slice of graphify's graph.json this layer reads.
type graphFile struct {
	Nodes []graphNode `json:"nodes"`
	Links []graphLink `json:"links"`
}

type graphNode struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	FileType string `json:"file_type"`
	File     string `json:"source_file"`
	Location string `json:"source_location"`
	Type     string `json:"type"`
}

type graphLink struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	Relation   string `json:"relation"`
	Confidence string `json:"confidence"`
	Context    string `json:"context"`
}

// IngestGraph loads a graphify graph.json into the symbol layer for one repo: it drops that repo's
// symbols and re-inserts the graph's nodes. Scoped by repo, so ingesting one project leaves another's
// symbols untouched. Returns the node count. (Edges are a later layer.)
func (s *Store) IngestGraph(repo, path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var g graphFile
	if err := json.Unmarshal(b, &g); err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM symbols WHERE repo = ?`, repo); err != nil {
		return 0, err
	}
	for _, n := range g.Nodes {
		if _, err := tx.Exec(`INSERT INTO symbols (repo, sid, label, kind, file, line) VALUES (?,?,?,?,?,?)
ON CONFLICT(repo, sid) DO UPDATE SET label=excluded.label, kind=excluded.kind, file=excluded.file, line=excluded.line`,
			repo, n.ID, n.Label, symbolKind(n), n.File, n.Location); err != nil {
			return 0, err
		}
	}
	return len(g.Nodes), tx.Commit()
}

// symbolKind reduces a graphify node to a coarse kind: its own type when it has one (a package), a
// document, a callable (label ends in "()"), a file (a first-line node whose label is a filename),
// else a plain symbol.
func symbolKind(n graphNode) string {
	switch {
	case len(n.Type) > 0:
		return n.Type
	case n.FileType == "document":
		return "doc"
	case strings.HasSuffix(n.Label, "()"):
		return "func"
	case n.Location == "L1" && strings.Contains(n.Label, "."):
		return "file"
	default:
		return "symbol"
	}
}

// Symbols lists a repo's symbol nodes, by file then line.
func (s *Store) Symbols(repo string) ([]Symbol, error) {
	return s.scanSymbols(`WHERE repo = ? ORDER BY file, line`, repo)
}

// GetSymbol returns one symbol by (repo, sid).
func (s *Store) GetSymbol(repo, sid string) (Symbol, bool, error) {
	ss, err := s.scanSymbols(`WHERE repo = ? AND sid = ?`, repo, sid)
	if err != nil || len(ss) == 0 {
		return Symbol{}, false, err
	}
	return ss[0], true, nil
}

func (s *Store) scanSymbols(where string, args ...any) ([]Symbol, error) {
	rows, err := s.db.Query(`SELECT repo, sid, label, kind, file, line FROM symbols `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Symbol
	for rows.Next() {
		var sym Symbol
		if err := rows.Scan(&sym.Repo, &sym.SID, &sym.Label, &sym.Kind, &sym.File, &sym.Line); err != nil {
			return nil, err
		}
		out = append(out, sym)
	}
	return out, rows.Err()
}
