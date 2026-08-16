# todo

A backlog and a wiki in one SQLite file, reachable three ways: a command-line tool for a person and
a pipe, an interactive terminal UI, and an MCP server for an LLM. The three are fronts over one core,
so what you add at the command line an assistant sees over MCP, and what an assistant records shows
up in the UI. Nothing is stored anywhere but the one database file, and the tool is a single static
binary with no cgo, so it installs and runs without a toolchain of its own.

It grew out of replacing a code-knowledge-graph indexer on a real project;
[COMPARISON.md](COMPARISON.md) is the measured account of that trade.

## Install

```
go install github.com/ks-tool/todo-mcp/cmd/todo@latest
```

That puts a `todo` binary in your `GOBIN` (usually `~/go/bin`).

## Quick start

```
todo add --epic Scheduler --priority P2 --tags api "add the QueueSort plugin"
todo next            # the single most urgent ready task
todo                 # no subcommand opens the interactive TUI
todo install         # wire the MCP server into a project's .mcp.json
```

## The model, in a sentence

A **task** has an epic, a priority, a body, tags and dependencies; a **doc** is a wiki page; tasks,
docs and commits map onto each other through links; deletion is always soft. An epic is just a
grouping string — a project is nothing but tasks under an epic, and an epic path like
`todo-mcp/graphify` nests one project inside another. See [docs/model.md](docs/model.md).

## The three faces

- **[Command line](docs/cli.md)** — a table at a terminal, JSON Lines into a pipe; `todo schema`
  prints the contract.
- **[Terminal UI](docs/tui.md)** — `todo tui`: list, detail pane, forms, and markdown export.
- **[MCP server](docs/mcp.md)** — `todo mcp`: typed tools over stdio, wired into a project with
  `todo install`.

Beyond the backlog it also checks **[API contracts](docs/contract.md)** between services (OpenAPI,
AsyncAPI, gRPC, GraphQL) and can ingest a **[code-symbol graph](docs/graphify.md)** to answer "how
do these relate" across intent and code.

Full documentation is in **[docs/](docs/README.md)**.

## Building from source

```
git clone https://github.com/ks-tool/todo-mcp
cd todo-mcp
go build ./cmd/todo    # or: go test ./...
```

The only dependencies are a pure-Go SQLite driver, the MCP SDK, a YAML parser, and the Charm
terminal stack (bubbletea, glamour, huh) — no cgo, so the result is one static binary.
