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

## What this tool deliberately does not do

There is no call graph here and there will not be one. "What calls X", "what breaks if Y changes"
— on a large unfamiliar codebase those are real questions, a knowledge graph is a real answer to
them, and grep is not. If that is your daily question, this tool does not compete; a language
server or a code-graph indexer does. Our finding was narrower than "graphs are bad": on a codebase
you know, asked by someone who knows a symbol name, the graph was a toll booth on the road to
grep — and the knowledge worth indexing was the *why* (decisions, designs, the backlog behind
them), not the *what* (the code, which is its own index).
