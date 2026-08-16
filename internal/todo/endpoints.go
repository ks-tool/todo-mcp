package todo

import "strings"

// The endpoint layer is the bridge across the network boundary. Each service's API surface — its
// (method, path) endpoints, each optionally bound to the code symbol that implements or calls it —
// is ingested per repo. Two services in one database then connect where their endpoints match: a
// path can run from a function in one, through the call it makes, across the boundary, to the
// handler in the other.

// StoredEndpoint is one endpoint of one service: its method and path, and the sid of the code symbol
// it binds to (via the spec's operationId), or empty when nothing bound.
type StoredEndpoint struct {
	Repo   string `json:"repo"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Symbol string `json:"symbol,omitempty"`
}

// SetEndpoints replaces a repo's endpoints — the second half of ingesting a service's API surface,
// scoped per repo like the symbols so one service never wipes another's.
func (s *Store) SetEndpoints(repo string, eps []StoredEndpoint) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM endpoints WHERE repo = ?`, repo); err != nil {
		return err
	}
	for _, e := range eps {
		if _, err := tx.Exec(`INSERT INTO endpoints (repo, method, path, symbol) VALUES (?,?,?,?)
ON CONFLICT(repo, method, path) DO UPDATE SET symbol=excluded.symbol`,
			repo, e.Method, e.Path, e.Symbol); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// endpointsScoped returns the endpoints for one repo, or all of them ("") — the graph loads all so a
// path can cross between services.
func (s *Store) endpointsScoped(repo string) ([]StoredEndpoint, error) {
	where, args := "", []any(nil)
	if len(repo) > 0 {
		where, args = "WHERE repo = ?", []any{repo}
	}
	rows, err := s.db.Query(`SELECT repo, method, path, symbol FROM endpoints `+where+` ORDER BY repo, method, path`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []StoredEndpoint
	for rows.Next() {
		var e StoredEndpoint
		if err := rows.Scan(&e.Repo, &e.Method, &e.Path, &e.Symbol); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Endpoints lists a repo's endpoints.
func (s *Store) Endpoints(repo string) ([]StoredEndpoint, error) { return s.endpointsScoped(repo) }

// BindEndpoints turns a spec's endpoints into stored rows, binding each to the code symbol whose name
// matches its operationId — matched on a NORMALIZED name (case and separators dropped), so an API's
// `createUser` binds a Go `CreateUser` and a Python `create_user` alike. That is what lets a path
// cross between services written in different languages.
func (s *Store) BindEndpoints(repo string, eps []Endpoint) ([]StoredEndpoint, error) {
	syms, err := s.symbolsScoped(repo)
	if err != nil {
		return nil, err
	}
	byNorm := make(map[string]string, len(syms))
	for _, sym := range syms {
		if n := normName(sym.Label); len(n) > 0 {
			byNorm[n] = sym.SID
		}
	}
	out := make([]StoredEndpoint, 0, len(eps))
	for _, e := range eps {
		r := StoredEndpoint{Repo: repo, Method: e.Method, Path: e.Path}
		if len(e.OpID) > 0 {
			r.Symbol = byNorm[normName(e.OpID)]
		}
		out = append(out, r)
	}
	return out, nil
}

// normName reduces a name to letters and digits, lower-cased — so createUser, CreateUser and
// create_user() all collapse to the same key.
func normName(s string) string {
	var b []rune
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b = append(b, r)
		}
	}
	return string(b)
}
