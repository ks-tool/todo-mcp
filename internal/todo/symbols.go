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
	if _, err := tx.Exec(`DELETE FROM symbol_edges WHERE repo = ?`, repo); err != nil {
		return 0, err
	}
	for _, n := range g.Nodes {
		if _, err := tx.Exec(`INSERT INTO symbols (repo, sid, label, kind, file, line) VALUES (?,?,?,?,?,?)
ON CONFLICT(repo, sid) DO UPDATE SET label=excluded.label, kind=excluded.kind, file=excluded.file, line=excluded.line`,
			repo, n.ID, n.Label, symbolKind(n), n.File, n.Location); err != nil {
			return 0, err
		}
	}
	for _, l := range g.Links {
		if _, err := tx.Exec(`INSERT INTO symbol_edges (repo, source, target, relation, confidence, context) VALUES (?,?,?,?,?,?)
ON CONFLICT(repo, source, target, relation) DO UPDATE SET confidence=excluded.confidence, context=excluded.context`,
			repo, l.Source, l.Target, l.Relation, l.Confidence, l.Context); err != nil {
			return 0, err
		}
	}
	return len(g.Nodes), tx.Commit()
}

// SymbolEdge is one code edge: a relation between two symbols and how sure the extractor is
// (EXTRACTED from the AST, or INFERRED by heuristic).
type SymbolEdge struct {
	Repo       string `json:"repo"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	Relation   string `json:"relation"`
	Confidence string `json:"confidence"`
	Context    string `json:"context,omitempty"`
}

// FindSymbol resolves a symbol from a reference: an exact node id, then an exact label (with or
// without the trailing "()"), then a substring of the label. repo "" searches every repo. It is how
// `explain APIRouter` finds the node named that.
func (s *Store) FindSymbol(repo, query string) (Symbol, bool, error) {
	q := strings.TrimSpace(query)
	repoCond, repoArgs := "", []any(nil)
	if len(repo) > 0 {
		repoCond, repoArgs = " AND repo = ?", []any{repo}
	}
	// Anchored forms first (id, whole label, the callable name in its plain / method / qualified
	// spellings), then a loose substring as the last resort.
	for _, cond := range []string{
		"sid = ?",
		"lower(label) = lower(?)",
		"lower(label) = lower(? || '()')",
		"lower(label) = lower('.' || ? || '()')",
		"lower(label) LIKE lower('%.' || ? || '()')",
		"label LIKE '%' || ? || '%'",
	} {
		ss, err := s.scanSymbols("WHERE "+cond+repoCond+" ORDER BY kind, file, line LIMIT 1", append([]any{q}, repoArgs...)...)
		if err != nil {
			return Symbol{}, false, err
		}
		if len(ss) > 0 {
			return ss[0], true, nil
		}
	}
	return Symbol{}, false, nil
}

// symbolByName is FindSymbol without the loose substring — an anchored match on the callable name in
// any repo. It is what path resolution uses, so a fuzzy phrase falls through to full-text search
// instead of latching onto a symbol whose name merely contains the words.
func (s *Store) symbolByName(query string) (Symbol, bool, error) {
	q := strings.TrimSpace(query)
	ss, err := s.scanSymbols(`WHERE sid = ?
   OR lower(label) = lower(?)
   OR lower(label) = lower(? || '()')
   OR lower(label) = lower('.' || ? || '()')
   OR lower(label) LIKE lower('%.' || ? || '()')
ORDER BY kind, file, line LIMIT 1`, q, q, q, q, q)
	if err != nil || len(ss) == 0 {
		return Symbol{}, false, err
	}
	return ss[0], true, nil
}

// SymbolConn is one connection in an explain view: which way the edge points from the node, its
// relation and confidence, and the neighbour it reaches.
type SymbolConn struct {
	Dir        string `json:"dir"` // out | in
	Relation   string `json:"relation"`
	Confidence string `json:"confidence"`
	SID        string `json:"sid"`
	Label      string `json:"label"`
}

// SymbolExplain is a node with its source, degree and connections — the graphify `explain` view.
type SymbolExplain struct {
	Symbol Symbol       `json:"symbol"`
	Degree int          `json:"degree"`
	Conns  []SymbolConn `json:"connections"`
}

// Explain resolves query to a symbol and returns it with its connections, each neighbour resolved to
// its label. It is the todo-mcp side of `graphify explain`, over the ingested graph.
func (s *Store) Explain(repo, query string) (*SymbolExplain, bool, error) {
	sym, ok, err := s.FindSymbol(repo, query)
	if err != nil || !ok {
		return nil, ok, err
	}
	edges, err := s.SymbolEdges(sym.Repo, sym.SID)
	if err != nil {
		return nil, false, err
	}
	ex := &SymbolExplain{Symbol: sym, Degree: len(edges)}
	for _, e := range edges {
		c := SymbolConn{Relation: e.Relation, Confidence: e.Confidence}
		if e.Source == sym.SID {
			c.Dir, c.SID = "out", e.Target
		} else {
			c.Dir, c.SID = "in", e.Source
		}
		if n, ok, _ := s.GetSymbol(sym.Repo, c.SID); ok {
			c.Label = n.Label
		} else {
			c.Label = c.SID
		}
		ex.Conns = append(ex.Conns, c)
	}
	return ex, true, nil
}

// SymbolEdges returns every edge touching sid in a repo — out of it or into it — which is a node's
// neighbourhood for an explain view.
func (s *Store) SymbolEdges(repo, sid string) ([]SymbolEdge, error) {
	return s.scanSymbolEdges(`WHERE repo = ? AND (source = ? OR target = ?) ORDER BY relation, target`, repo, sid, sid)
}

// symbolsScoped and symbolEdgesScoped feed the path graph: a repo when the scope names one, else all.
func (s *Store) symbolsScoped(repo string) ([]Symbol, error) {
	if len(repo) > 0 {
		return s.scanSymbols(`WHERE repo = ? ORDER BY file, line`, repo)
	}
	return s.scanSymbols(`ORDER BY file, line`)
}

func (s *Store) symbolEdgesScoped(repo string) ([]SymbolEdge, error) {
	if len(repo) > 0 {
		return s.scanSymbolEdges(`WHERE repo = ? ORDER BY source`, repo)
	}
	return s.scanSymbolEdges(`ORDER BY source`)
}

func (s *Store) scanSymbolEdges(where string, args ...any) ([]SymbolEdge, error) {
	rows, err := s.db.Query(`SELECT repo, source, target, relation, confidence, context FROM symbol_edges `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SymbolEdge
	for rows.Next() {
		var e SymbolEdge
		if err := rows.Scan(&e.Repo, &e.Source, &e.Target, &e.Relation, &e.Confidence, &e.Context); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
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
