# Cross-service path — worked example

Two services talk over an HTTP API. `service-a` (an orders service) calls the users API; `service-b`
(the users service) implements it. This example shows `todo path` running from a function in one
service, across the network boundary, to a function in the other.

Each service is given as it would be in real life: a **code graph** (`graph.json`, as extracted by
graphify) and an **API spec** (`openapi.yaml`). The spec's `operationId` (`createUser`) is what binds
an endpoint to the function that calls or handles it.

```
service-a/                       service-b/
  graph.json  PlaceOrder → createUser (client)      graph.json  createUser (handler) → saveUser (store)
  openapi.yaml  POST /users  operationId: createUser  openapi.yaml  POST /users  operationId: createUser
```

## Run it

```sh
DB=$(mktemp -u).db

# 1. ingest each service's code graph, under its own repo label
todo --db "$DB" symbols service-a --graph service-a/graph.json --repo orders
todo --db "$DB" symbols service-b --graph service-b/graph.json --repo users

# 2. ingest each service's API endpoints; operationId binds them to the code symbols
todo --db "$DB" endpoints service-a/openapi.yaml --repo orders
todo --db "$DB" endpoints service-b/openapi.yaml --repo users

# 3. ask for the path across the two services
todo --db "$DB" path orders:PlaceOrder users:saveUser
```

## The path

```
symbol    PlaceOrder()
  --calls   -> symbol    createUser()          # orders' generated client
  --endpoint-> endpoint  POST /users (orders)   # bound by operationId
  --boundary-> endpoint  POST /users (users)    # the network hop — a contract match
  --endpoint-> symbol    createUser()          # users' handler
  --calls   -> symbol    saveUser()            # users' storage
```

The `boundary` edge is where the request leaves one service and arrives at the other: two endpoints
with the same `(method, path)` in different repos — exactly what `todo contract` matches. The two
`endpoint` edges are the operationId bindings; the `calls` edges are ordinary code edges from the
graph. No service directly calls the other's functions — the path is stitched from the call it makes,
the contract that matches it, and the handler that serves it.

## Addressing

`orders:PlaceOrder` is a **repo-qualified symbol** — `repo:name` — so a symbol is named unambiguously
when several services share one database. A bare name still works when it is unique.
