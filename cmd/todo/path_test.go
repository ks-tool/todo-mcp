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

func TestPathMermaid(t *testing.T) {
	p := &todo.Path{
		Start: todo.PathNode{Kind: todo.KindSymbol, ID: "client_place_order", Repo: "orders", Label: "place_order()"},
		Steps: []todo.PathStep{
			{Edge: "calls", Node: todo.PathNode{Kind: todo.KindSymbol, ID: "client_create_user", Repo: "orders", Label: "create_user()"}},
			{Edge: "endpoint", Node: todo.PathNode{Kind: todo.KindEndpoint, ID: "orders:POST /users", Repo: "orders", Label: "POST /users"}},
			{Edge: "boundary", Node: todo.PathNode{Kind: todo.KindEndpoint, ID: "users:POST /users", Repo: "users", Label: "POST /users"}},
			{Edge: "endpoint", Node: todo.PathNode{Kind: todo.KindSymbol, ID: "server_createuser", Repo: "users", Label: "CreateUser()"}},
			{Edge: "calls", Node: todo.PathNode{Kind: todo.KindSymbol, ID: "server_saveuser", Repo: "users", Label: "SaveUser()"}},
		},
	}
	out := pathMermaid(p)
	for _, want := range []string{
		"flowchart LR",
		`subgraph svc_orders["@orders"]`,
		`subgraph svc_users["@users"]`,
		`n0["place_order()<br/><i>symbol</i>"]`,
		"-.->|boundary|", // the network hop is dotted
		"n4 -->|calls| n5",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("mermaid output missing %q:\n%s", want, out)
		}
	}
	// the start symbol (orders) must be declared inside the orders subgraph, before users opens
	if i, j := strings.Index(out, "n0["), strings.Index(out, `subgraph svc_users`); i < 0 || j < 0 || i > j {
		t.Errorf("n0 must be declared under @orders, before @users opens:\n%s", out)
	}
}

func TestMermaidTextAndID(t *testing.T) {
	if got := mermaidText(`a<b>&"c"`); got != "a&lt;b&gt;&amp;&quot;c&quot;" {
		t.Errorf("mermaidText escape: %q", got)
	}
	if got := mermaidID("todo-mcp"); got != "svc_todo_mcp" {
		t.Errorf("mermaidID(todo-mcp) = %q, want svc_todo_mcp", got)
	}
}
