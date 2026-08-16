package todo

import (
	"database/sql"
	"strings"
)

// The graph is the whole point of keeping tasks and trailers in one place: `todo path A B` answers
// "how do these two relate", walking the edges that already exist — a task to the commits that
// closed it, a commit to its parents, a task to the tasks it depends on, a task or a doc to the
// pages it links. Nodes are tasks (id), trailers (sha) and docs (id); the path is the shortest
// chain of those edges, found by breadth-first search so the first route reached is a shortest one.

// PathNode is one node on a path — its kind, its handle (task id, trailer sha, or doc id) and a
// short label to read it by.
type PathNode struct {
	Kind  string `json:"kind"` // task | trailer | doc
	ID    string `json:"id"`
	Label string `json:"label"`
}

// PathStep is one edge crossed and the node it arrives at.
type PathStep struct {
	Edge string   `json:"edge"` // commit | parent | dep | doc
	Node PathNode `json:"node"`
}

// Path is a resolved route: the start node and the steps from it to the end.
type Path struct {
	Start PathNode   `json:"start"`
	Steps []PathStep `json:"steps"`
}

// PathScope narrows which nodes take part — an epic (a project) or tags (cross-cutting slices), the
// two axes the backlog filters on. A zero scope walks the whole graph.
type PathScope struct {
	Epic string
	Tags []string
}

const (
	edgeCommit = "commit" // task — trailer, the commit that closed the task
	edgeParent = "parent" // trailer — trailer, git ancestry
	edgeDep    = "dep"    // task — task, a dependency edge
	edgeDoc    = "doc"    // task — doc / doc — doc, a wiki link
)

// Path resolves a and b to nodes and returns the shortest chain of edges between them. a and b are
// each a task id, a trailer sha (full or a 7+ char prefix), a doc id, or — failing an exact handle —
// a full-text phrase, resolved to the node that mentions it most. Found is false when either end
// does not resolve or no path connects them.
func (s *Store) Path(a, b string, scope PathScope) (p *Path, found bool, err error) {
	start, ok, err := s.resolveNode(a)
	if err != nil || !ok {
		return nil, false, err
	}
	end, ok, err := s.resolveNode(b)
	if err != nil || !ok {
		return nil, false, err
	}
	g, err := s.buildGraph(scope)
	if err != nil {
		return nil, false, err
	}
	// Either endpoint may sit outside the scoped node set; put it back so a path can start and end
	// on it even when the scope would have excluded it.
	g.ensure(start)
	g.ensure(end)

	route, ok := g.bfs(nodeKey(start), nodeKey(end))
	if !ok {
		return nil, false, nil
	}
	out := &Path{Start: g.nodes[route[0].key]}
	for _, hop := range route[1:] {
		out.Steps = append(out.Steps, PathStep{Edge: hop.edge, Node: g.nodes[hop.key]})
	}
	return out, true, nil
}

// graph is the in-memory adjacency built for one Path call: the nodes by key and, per node, the
// edges out of it (undirected — every edge is added both ways, so a shortest path can cross it in
// either direction).
type graph struct {
	nodes map[string]PathNode
	adj   map[string][]edge
}

type edge struct {
	to   string
	kind string
}

type hop struct {
	key  string
	edge string
}

// crumb is the breadcrumb left at a node the first time BFS reaches it: which node it came from and
// over which edge, so the route can be walked back from the end.
type crumb struct {
	prev string
	edge string
}

func nodeKey(n PathNode) string { return n.Kind + ":" + n.ID }

func (g *graph) ensure(n PathNode) {
	if _, ok := g.nodes[nodeKey(n)]; !ok {
		g.nodes[nodeKey(n)] = n
	}
}

func (g *graph) link(a, b PathNode, kind string) {
	g.ensure(a)
	g.ensure(b)
	ka, kb := nodeKey(a), nodeKey(b)
	g.adj[ka] = append(g.adj[ka], edge{to: kb, kind: kind})
	g.adj[kb] = append(g.adj[kb], edge{to: ka, kind: kind})
}

// bfs finds a shortest path from start to end and returns it as hops (the first hop is the start
// itself, with no edge). It explores in insertion order, so the route is deterministic given the
// same graph.
func (g *graph) bfs(start, end string) ([]hop, bool) {
	if start == end {
		return []hop{{key: start}}, true
	}
	seen := map[string]crumb{start: {}}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.adj[cur] {
			if _, ok := seen[e.to]; ok {
				continue
			}
			seen[e.to] = crumb{prev: cur, edge: e.kind}
			if e.to == end {
				return rebuild(seen, start, end), true
			}
			queue = append(queue, e.to)
		}
	}
	return nil, false
}

func rebuild(seen map[string]crumb, start, end string) []hop {
	var rev []hop
	for k := end; ; {
		c := seen[k]
		rev = append(rev, hop{key: k, edge: c.edge})
		if k == start {
			break
		}
		k = c.prev
	}
	// rev holds end→start; flip it so the caller reads start→end.
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// buildGraph loads the nodes and edges once for a Path call. Scope filters the task and trailer
// node sets; edges to a node the scope excluded simply do not resolve, which keeps a path inside the
// chosen project or slice.
func (s *Store) buildGraph(scope PathScope) (*graph, error) {
	g := &graph{nodes: map[string]PathNode{}, adj: map[string][]edge{}}

	tasks, err := s.List(Filter{Epic: scope.Epic, Tags: scope.Tags})
	if err != nil {
		return nil, err
	}
	taskIn := map[string]bool{}
	for _, t := range tasks {
		g.ensure(taskNode(t))
		taskIn[t.ID] = true
	}
	trailers, err := s.TrailersFiltered(scope.Epic, scope.Tags)
	if err != nil {
		return nil, err
	}
	trailerIn := map[string]bool{}
	for _, tr := range trailers {
		g.ensure(trailerNode(tr))
		trailerIn[tr.SHA] = true
	}
	for _, tr := range trailers {
		for _, par := range tr.Parents {
			if trailerIn[par] {
				g.link(trailerNode(tr), PathNode{Kind: KindTrailer, ID: par, Label: shortLabel(par)}, edgeParent)
			}
		}
	}

	// dep edges — both ends must be tasks in scope.
	depRows, err := s.db.Query(`SELECT task_id, depends_on FROM deps`)
	if err != nil {
		return nil, err
	}
	if err := scanPairs(depRows, func(a, b string) {
		if taskIn[a] && taskIn[b] {
			g.link(PathNode{Kind: KindTask, ID: a}, PathNode{Kind: KindTask, ID: b}, edgeDep)
		}
	}); err != nil {
		return nil, err
	}

	// link edges — commit (task→trailer) and doc (task or doc → doc).
	linkRows, err := s.db.Query(`SELECT task_id, kind, ref FROM links WHERE kind IN (?, ?)`, LinkCommit, LinkDoc)
	if err != nil {
		return nil, err
	}
	defer func() { _ = linkRows.Close() }()
	for linkRows.Next() {
		var from, kind, ref string
		if err := linkRows.Scan(&from, &kind, &ref); err != nil {
			return nil, err
		}
		switch kind {
		case LinkCommit:
			if taskIn[from] && trailerIn[ref] {
				g.link(PathNode{Kind: KindTask, ID: from}, PathNode{Kind: KindTrailer, ID: ref, Label: shortLabel(ref)}, edgeCommit)
			}
		case LinkDoc:
			// from is a task or a doc; ref is always a doc. Attach whichever end is present.
			if src, ok := g.nodes[KindTask+":"+from]; ok {
				g.linkDoc(src, ref)
			} else if d, ok, _ := s.GetDoc(from); ok {
				g.linkDoc(docNode(d), ref)
			}
		}
	}
	return g, linkRows.Err()
}

// linkDoc joins a source node to a doc by id, materializing the doc node from the store so it reads
// by its title.
func (g *graph) linkDoc(src PathNode, docID string) {
	dst := PathNode{Kind: KindDoc, ID: docID, Label: docID}
	if n, ok := g.nodes[KindDoc+":"+docID]; ok {
		dst = n
	}
	g.link(src, dst, edgeDoc)
}

const KindDoc = "doc"

func taskNode(t Task) PathNode {
	return PathNode{Kind: KindTask, ID: t.ID, Label: oneLineLabel(t.Text)}
}
func trailerNode(t Trailer) PathNode {
	return PathNode{Kind: KindTrailer, ID: t.SHA, Label: t.Subject}
}
func docNode(d Doc) PathNode {
	return PathNode{Kind: KindDoc, ID: d.ID, Label: d.Title}
}

// resolveNode turns a user's reference into a node: an exact task id, doc id or trailer sha (full or
// a unique 7+ char prefix) first, then a full-text phrase resolved to the task or trailer that
// mentions it most. The exact handles are tried before the search so a real id is never shadowed by
// a coincidental text match.
func (s *Store) resolveNode(ref string) (PathNode, bool, error) {
	if t, ok, err := s.Get(ref); err != nil {
		return PathNode{}, false, err
	} else if ok {
		return taskNode(t), true, nil
	}
	if d, ok, err := s.GetDoc(ref); err != nil {
		return PathNode{}, false, err
	} else if ok {
		return docNode(d), true, nil
	}
	if tr, ok, err := s.GetTrailer(ref); err != nil {
		return PathNode{}, false, err
	} else if ok {
		return trailerNode(tr), true, nil
	}
	if len(ref) >= 7 {
		if tr, ok, err := s.trailerByPrefix(ref); err != nil {
			return PathNode{}, false, err
		} else if ok {
			return trailerNode(tr), true, nil
		}
	}
	return s.resolveByText(ref)
}

func (s *Store) trailerByPrefix(prefix string) (Trailer, bool, error) {
	ts, err := s.scanTrailers(`WHERE sha LIKE ? || '%' LIMIT 2`, prefix)
	if err != nil || len(ts) != 1 {
		return Trailer{}, false, err
	}
	return ts[0], true, nil
}

// resolveByText picks the single node a phrase mentions most: the best-scoring task, or failing
// that the best-scoring trailer. A task wins ties because intent is what a person names a path by.
func (s *Store) resolveByText(ref string) (PathNode, bool, error) {
	q := ftsQuery(ref)
	if len(q) == 0 {
		return PathNode{}, false, nil
	}
	var id string
	err := s.db.QueryRow(`SELECT t.id FROM tasks_fts f JOIN tasks t ON t.rowid = f.rowid
WHERE f.tasks_fts MATCH ? AND t.deleted_at = '' ORDER BY bm25(f.tasks_fts) LIMIT 1`, q).Scan(&id)
	if err == nil {
		if t, ok, err := s.Get(id); err == nil && ok {
			return taskNode(t), true, nil
		}
	} else if err != sql.ErrNoRows {
		return PathNode{}, false, err
	}
	var sha string
	err = s.db.QueryRow(`SELECT f.sha FROM trailers_fts f
WHERE f.trailers_fts MATCH ? ORDER BY bm25(f.trailers_fts) LIMIT 1`, q).Scan(&sha)
	if err == sql.ErrNoRows {
		return PathNode{}, false, nil
	}
	if err != nil {
		return PathNode{}, false, err
	}
	if tr, ok, err := s.GetTrailer(sha); err == nil && ok {
		return trailerNode(tr), true, nil
	}
	return PathNode{}, false, nil
}

func oneLineLabel(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func shortLabel(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// scanPairs runs fn over each two-column row and closes the rows.
func scanPairs(rows *sql.Rows, fn func(a, b string)) error {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return err
		}
		fn(a, b)
	}
	return rows.Err()
}
