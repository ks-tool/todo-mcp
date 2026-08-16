package main

import (
	"strings"
	"testing"

	"github.com/ks-tool/todo-mcp/internal/todo"
)

func TestPathNodeLineShowsRepo(t *testing.T) {
	sym := pathNodeLine(todo.PathNode{Kind: todo.KindSymbol, ID: "client_create_user", Repo: "orders", Label: "create_user()"})
	if !strings.Contains(sym, "create_user()") || !strings.Contains(sym, "@orders") {
		t.Errorf("symbol line must show label and @repo: %q", sym)
	}
	ep := pathNodeLine(todo.PathNode{Kind: todo.KindEndpoint, ID: "users:POST /users", Repo: "users", Label: "POST /users"})
	if !strings.Contains(ep, "POST /users") || !strings.Contains(ep, "@users") {
		t.Errorf("endpoint line must show path and @repo: %q", ep)
	}
	if got := pathNodeLine(todo.PathNode{Kind: todo.KindTask, ID: "todomcp-01", Label: "do a thing"}); strings.Contains(got, "@") {
		t.Errorf("a repo-less node must not show @: %q", got)
	}
}
