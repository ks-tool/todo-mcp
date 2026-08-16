# Why this instead of a code knowledge graph

This tool exists because we replaced one. For several months a project of ours ran graphify, a
code-knowledge-graph indexer: it parsed the whole tree into a symbol graph — functions, types, call edges, plus nodes for the design documents — and
served an AI assistant questions like "where does the agent create its user namespace" by graph
traversal. The idea is sound, and on an unfamiliar codebase a call graph answers questions plain
search cannot. What follows is not an argument that such tools are bad; it is an account of what we
measured on one real project, and where the trade actually landed.

## What we measured

The graph held two kinds of knowledge, and they earned their keep very differently.

The **document half** worked. The design docs were indexed, findable by full-text query, and the
assistant reached for them constantly — that half of the graph was consulted daily and missed when
it was gone.

The **code half** cost more than it returned. Three specific costs, all from one working session:

- **Query noise.** A question about user namespaces returned 257 nodes, truncated to 79 for the
  token budget, with the note "the answer may be among the 178 cut nodes" — and half of the shown
  nodes were from the storage layer, which had nothing to do with the question. The assistant's own
  next step, each time, was `rg CLONE_NEWUSER`, which returned the exact files in 16 milliseconds.
  The pattern repeated: whenever the asker already knew a symbol name, plain search beat the graph,
  and in day-to-day work on a familiar codebase the asker almost always knows a symbol name.
- **The index destroyed work once.** The reindex command, run as prescribed after an edit, deleted
  an entire source directory from the working copy — 44 files, staged as deletions. Committed
  content came back from git; uncommitted edits in those files were gone. After that, the project's
  instructions file had to carry a NEVER-run warning about the tool's own refresh command, which is
  a strange thing for a productivity tool's documentation to need.
- **Standing overhead.** The graph directory weighed 75 MB and respawned its cache in the repo root
  however often it was deleted. A pre-tool hook required a graph query before every text search,
  taxing the operation that was actually working to subsidize the one that was not. And the
  instructions section explaining the tool's traps — which flags lose which nodes, why the update
  command must never see an uncommitted tree — had grown into the longest section of the file.

## What replaced it

The observation was that the half worth keeping — searchable design docs — is not a graph problem
at all, and the half that hurt — code navigation — was already better served by `rg`. So:

- The design docs became **wiki pages** in this tool's SQLite file: full-text searchable, and — the
  part the graph never had — **mapped to the backlog**. A task knows its design docs; a doc knows
  its tasks; both know their commits. On the project in question, 314 task↔doc edges were created
  automatically, because the backlog's tasks already named the doc each belonged to.
- Code questions went back to plain search, stated as policy in the project instructions: grep by
  the symbol you know. No index to keep fresh, no hook, nothing that can be stale, nothing that
  writes outside its own database.
- The answers became **structured**. The graph returned truncated text dumps; the MCP tools here
  return typed JSON an assistant acts on directly — which also surfaced its own lesson: the first
  real search through the MCP front returned 227 KB of document bodies, the same disease in our own
  code, and lists have returned metadata-only ever since.

The footprint went from 75 MB to 2.3 MB, and the risk class went from "the refresh can delete
source" to "the tool writes only its own database file".

## Where the code graph came back — and how it is different

The account above ended, for a long time, with "there is no call graph here and there will not be
one." That is no longer true, and it is worth being honest about why the line changed and why it is
not a reversal of the lesson.

A code graph did come back, under the sub-project `todo-mcp/graphify`. What changed is every property
that made the original one a net loss:

- **Ingested, not owned.** todo does not parse the tree or maintain an index. It runs an external
  extractor (graphify) on demand and loads its `graph.json` into the same SQLite file. There is no
  standing 75 MB cache respawning in the repo root, no pre-search hook, no refresh command of its own
  that can go wrong.
- **Non-destructive by construction.** The disaster that justified the original line — a reindex that
  deleted 44 source files — cannot happen here. todo's `reindex` rebuilds only its own derived tables
  (the trailer and symbol caches), scoped **per repo** so one project never wipes another's, and
  `install` refuses to reindex silently: when a repo has history it tells you to `todo backup` first.
  The tool writes only its own database file.
- **Optional.** The backlog, the wiki and the provenance graph work with no code layer at all. The
  symbols are a layer you ingest when a question wants them, not a tax on every search.
- **Unified, which is the actual point.** A standalone code graph answers "what calls X". The reason
  to put it *here* is that the code nodes join the intent and provenance already in the database —
  bridged at the file level, so `todo path` runs from a task to the commit that closed it to the file
  it touched to a symbol in that file. That crossing is the thing neither grep nor a standalone graph
  gives you.

And a second, narrower use of the same idea: **[API-contract checking](docs/contract.md)**. Two
services tracked in one backlog, asked "is the contract still honoured", get one structured answer —
the list of orphan-calls and schema-drifts — instead of a text dump to read. It is the original
lesson applied, not abandoned: a specific question with a typed answer, not a graph traversal that
returns 257 nodes truncated to 79.

The core finding stands. Structured beats dumps; the knowledge worth keeping is the *why* as much as
the *what*; and no tool here rebuilds an index in a way that can lose your work. The code graph
earns its place now only because it obeys those rules — ingested, optional, safe, and joined to the
decisions rather than standing apart from them.
