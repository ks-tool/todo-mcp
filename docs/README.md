# todo — documentation

A backlog and a wiki in one SQLite file, reached three ways — a command-line tool, an interactive
terminal UI, and an MCP server for an LLM. The three are fronts over one core: what you add at the
command line an assistant sees over MCP, and what an assistant records shows up in the UI.

The [project README](../README.md) is the short introduction. These pages are the detail.

## Pages

- **[The model](model.md)** — tasks, docs, epics (and nested epics), tags, links, commits,
  soft-delete, and the git-native idea behind it all.
- **[Command line](cli.md)** — every command, the output contract, and the database-resolution
  order.
- **[Terminal UI](tui.md)** — the keys, the panes, and markdown export.
- **[MCP server](mcp.md)** — the tools, `todo install`, and the `.mcp.json` wiring.
- **[API contract checking](contract.md)** — `todo contract` across OpenAPI, AsyncAPI, gRPC and
  GraphQL: orphan-call and schema-drift.
- **[graphify — the code layer](graphify.md)** — ingesting a code-symbol graph and querying it with
  `explain` and `path`.
- **[Cross-service paths](cross-service.md)** — stitching two services' graphs across the network
  boundary so `todo path` runs from a function in one to a function in the other.

## Install

```
go install github.com/ks-tool/todo-mcp/cmd/todo@latest
```

That puts a `todo` binary in your `GOBIN` (usually `~/go/bin`). It is a single static binary with no
cgo, so it installs and runs without a toolchain of its own. Every command finds its database the
same way — see [Command line › the database](cli.md#the-database).

## Design notes

The wiki also holds the project's own design docs, reachable with `todo doc show` or in the TUI
(`w`). [COMPARISON.md](../COMPARISON.md) is the measured account of why this tool exists.
