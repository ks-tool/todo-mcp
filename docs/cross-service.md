# Cross-service paths

The [code layer](graphify.md) gives each service its own call graph. Two services that talk over an
API are, in code, two disconnected graphs: one never calls the other's functions — it makes a network
request. This feature stitches the two together, so `todo path` can run from a function in one
service, across the network boundary, to the function that serves it in the other.

There is a runnable [Go-server / Python-client example](../examples/cross-service-go-python/) that
crosses the network boundary and the language boundary at once.

## The three links

A cross-service path is made of three kinds of edge:

1. **code → endpoint** — a function binds to the endpoint it calls or handles, via the spec's
   `operationId` (or an rpc / GraphQL field name). The endpoint's `operationId` is matched to the
   code symbol whose name is the same.
2. **the boundary** — two endpoints with the same `(method, path)` in different services are a
   **contract match** (the same relation [`todo contract`](contract.md) reports). That match is the
   edge the request crosses over the network.
3. **endpoint → code** — the same binding on the other side, to the handler.

So a path reads: `function → the client/handler symbol → its endpoint → (boundary) → the other
service's endpoint → its symbol`.

## Using it

Ingest each service's **symbols** and its **API endpoints**, under a per-service `--repo` label:

```
todo symbols  <service-dir>  --graph graph.json  --repo <service>
todo endpoints <service-spec>                    --repo <service>
```

`todo endpoints` parses the spec (OpenAPI, AsyncAPI, gRPC `.proto`, or GraphQL) and binds each
endpoint to the symbol whose name matches its `operationId`. Then a repo-qualified path:

```
todo path <serviceA>:<function> <serviceB>:<function>
```

`<repo>:<name>` names a symbol unambiguously when several services share one database.

## Requirements and limits

- Each service's **symbols must be ingested first** — the binding attaches an endpoint to an existing
  symbol node.
- The binding relies on the **`operationId`** (rpc / field name) matching the code symbol's name,
  **normalized** — case and separators (`_`, `-`, `.`) dropped — so `createUser` binds a Go
  `CreateUser` and a Python `create_user` alike, and a path crosses between services written in
  different languages. A spec without operationIds still produces endpoint nodes and the boundary
  edge, but no code binding, so a path reaches the endpoint but not the function behind it.
- The boundary is matched by `(method, path)`, the same identity `todo contract` uses; schema drift on
  that endpoint is a [contract](contract.md) concern, separate from whether a path exists.
