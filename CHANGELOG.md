# Changelog

All notable changes to this project are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] — 2026-08-16

The first release. `todo` is a backlog and a wiki in one SQLite file, reachable three
ways — a command-line tool, an interactive terminal UI, and an MCP server for an LLM.
The three are fronts over one core, so what you add at the command line an assistant
sees over MCP, and what an assistant records shows up in the UI. Everything lives in the
one database file, and the tool is a single static binary with no cgo, so it installs
and runs without a toolchain of its own.

### The model

- **Tasks** carry an epic, a priority, a body, tags, and dependencies; **docs** are wiki
  pages; **comment threads** hang off tasks — timestamped, author-less, and editable on a
  done task without reopening it.
- Two orthogonal axes: an **epic is a project** (each task single-homed), and **tags are
  cross-cutting slices** (many-to-many). An epic path like `todo-mcp/graphify` nests one
  project inside another, and a filter on the parent includes the nested tasks.
- **Two-layer storage.** The *authored* layer (tasks, docs, comments, links, trailer→epic
  bindings) is persistent, soft-deleted to a restorable trash, and never touched by a
  reindex; the *derived* layer (git trailers, code symbols, edges) is a cache, rebuilt on
  demand. One database can hold **several projects at once**, and a per-repo reindex never
  wipes another project's data.
- **Deletion is always soft** — trash and restore — so nothing is lost to a wrong call.

### Three fronts

- **Command line** — a table at a terminal, JSON Lines into a pipe or under `--json`;
  `todo schema` prints the machine contract. Covers tasks (`add`, `list`, `next`,
  `ready`, `show`, `edit`, `done`, `reopen`, `dep`), docs, comment threads, trailers,
  `backup`, and `install`.
- **Terminal UI** (`todo tui`) — a list with a detail pane; add / edit / comment / commit
  / link forms, each cancelable with `Esc`; filters by epic, tag, status, and full-text
  search; live refresh (`ctrl+r`) to pick up tickets added from outside the current
  filter; a `?` help screen; and markdown export (`x`) of the filtered tasks or the wiki.
- **MCP server** (`todo mcp`) — typed tools over stdio, wired into a project with
  `todo install`, which writes the `.mcp.json` entry and a CLAUDE.md block of working
  rules for an assistant.

### Git-native provenance

- `todo reindex` builds a trailer (commit) layer from `git log`, **per repo**, incrementally
  by ref shift; it runs from git hooks, on `todo mcp` start, or by hand.
- Trailer→epic bindings and commit tags are local and survive a reindex. `task_commit`
  links a commit to the task it closed and is multi-repo aware — a sha is accepted when a
  reindex has already seen it, or when the caller names its repo with `dir`.
- `todo path A B` walks the shortest chain of edges between two nodes across tasks,
  commits, docs, files, and symbols — from an intent to the commits behind it, a commit to
  its ancestry, a task to what it touched.

### Code graph (optional, via graphify ingestion)

- `todo symbols <dir>` ingests a code-symbol graph — nodes and edges with their
  `EXTRACTED` / `INFERRED` confidence — using [graphify](https://github.com/graphify) as
  the extractor. todo ingests it per repo, non-destructively; it never runs its own static
  analysis.
- `todo explain <node>` reports a symbol's source `file:line`, its degree, its typed
  connections, and its community.
- **Community detection** over the symbol graph (label propagation, no cgo).
- Symbols are first-class endpoints of `todo path`.

### API contracts

- `todo contract <consumer> <provider>` checks a service contract and reports **orphan
  calls** (the consumer calls an endpoint the provider dropped or renamed) and **schema
  drift** (the request/response shapes diverged), across four formats: **OpenAPI** (JSON
  and YAML), **AsyncAPI**, **gRPC** `.proto`, and **GraphQL** SDL.

### Cross-service paths

- `todo endpoints <spec> --repo <r>` ingests a service's API endpoints and binds each to
  the code symbol behind it on a **normalized** name, so an operationId `createUser` binds
  a Go `CreateUser` and a Python `create_user` alike.
- `todo path serviceA:Fn serviceB:Fn` runs from a function in one service, across the
  network boundary (a contract match on `method` + `path`), into the function that serves
  it in the other — across languages as well as the wire.
- `todo path … --mermaid` renders a path as a Mermaid flowchart: each service is a
  subgraph, and the network hop is drawn dotted.
- A runnable [Go-server / Python-client example](examples/cross-service-go-python/) crosses
  the network and language boundaries at once.

### Install

```
go install github.com/ks-tool/todo-mcp/cmd/todo@latest
```

Prebuilt binaries are attached to each release for **linux, darwin, and windows** on
**amd64 and arm64**, alongside a `SHA256SUMS` file. The build is pure-Go
(`CGO_ENABLED=0`, `-trimpath -ldflags="-s -w"`), so each is a single static binary. Built
with Go 1.26.

### Dependencies

A short list, all pure-Go: `modernc.org/sqlite` (the driver, no cgo), the
`modelcontextprotocol/go-sdk`, `go.yaml.in/yaml/v3`, and the Charm terminal stack
(bubbletea, bubbles, lipgloss, glamour, huh).

[1.0.0]: https://github.com/ks-tool/todo-mcp/releases/tag/v1.0.0
