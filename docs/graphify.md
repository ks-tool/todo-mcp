# graphify — the code layer

The backlog's graph is about *intent and provenance* — tasks, the commits behind them, the files
those commits touched. graphify adds an optional second graph about *code structure* — the symbols
(files, functions, packages) and the edges between them (`calls`, `imports`, `references`, …).

todo does not parse source itself. It **ingests** the graph that
[graphify](https://github.com/graphify) extracts, so the parsing is graphify's job and the querying
and unification are todo's.

```
todo symbols <dir>          # run graphify on the repo and load its graph, scoped by repo
todo symbols <dir> --graph graph.json   # or ingest a prebuilt graphify graph.json
```

The nodes go into a derived `symbols` table and the edges into `symbol_edges`, rebuilt per repo like
the trailers — so ingesting one project never wipes another's. Each edge keeps graphify's confidence
mark, `EXTRACTED` (from the AST) or `INFERRED` (by heuristic).

## explain

```
todo explain <node>
```

A symbol resolved by id, by label (with or without `()`), or by a label substring, printed the way
graphify does: its source `file:line`, its `Community` (a cluster id from label propagation over the
code edges), its `Degree`, and each connection with its direction, relation and confidence.

```
Node: reindexRepo()
  Source:    cmd/todo/main.go L955
  Community: 0
  Degree:    4
Connections (4):
  <-- frontCommands()  [calls] [EXTRACTED]
  ...
```

Available over MCP as `explain`.

## path — crossing the two graphs

The value of ingesting rather than shelling out is that code and provenance become **one** graph,
bridged at the file level: a symbol's source file is the same file node a commit touched. So `todo
path` can run from an intent to a symbol:

```
$ todo path <commit-sha> IngestGraph
trailer  "graphify: symbols and code edges join the path graph"
  --file--> file internal/todo/symbols.go
  --file--> symbol .IngestGraph()
```

`todo path A B` resolves each end as a task id, a commit sha, a doc id, a file path, an exact symbol
name, or — failing those — a full-text phrase, and returns the shortest chain of edges between them.
`--epic` / `--tag` scope which nodes take part.

## Community

After each ingest, label propagation over the code edges clusters the symbols into communities (no
cgo, no LLM). `explain` reports the community id, so architectural neighbourhoods are visible.

## Relationship to the project

The graphify work lives in this repository, under the sub-epic `todo-mcp/graphify` — an epic within
the `todo-mcp` project. See [COMPARISON.md](../COMPARISON.md) for why a code graph came back as an
ingested, optional, non-destructive layer rather than an index this tool owns and rebuilds.
