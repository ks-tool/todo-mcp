# Cross-service path — a Go server and a Python client

The same idea as the [first example](../cross-service/), but **across two languages**: the users
service is a Go HTTP server, and the orders service calls it from Python. `todo path` runs from a
Python function, over the network boundary, into a Go function.

```
client-py/  (repo: orders)                    server-go/  (repo: users)
  client.py   place_order → create_user         server.go   CreateUser → SaveUser
  graph.json  place_order() → create_user()     graph.json  CreateUser() → SaveUser()
  openapi.yaml  POST /users  createUser          openapi.yaml  POST /users  createUser
```

The interesting part is the binding. The API's operationId is `createUser`, but the Python client
function is idiomatic `create_user` and the Go handler is `CreateUser`. `todo endpoints` matches an
operationId to a symbol on a **normalized** name — case and separators dropped — so all three collapse
to `createuser` and bind. That is what makes the path cross languages.

## Run it

```sh
DB=$(mktemp -u).db

# each service: its code graph, then its API endpoints, under its own repo
todo --db "$DB" symbols   server-go --graph server-go/graph.json --repo users
todo --db "$DB" symbols   client-py --graph client-py/graph.json --repo orders
todo --db "$DB" endpoints server-go/openapi.yaml --repo users
todo --db "$DB" endpoints client-py/openapi.yaml --repo orders

todo --db "$DB" path orders:place_order users:SaveUser
```

## The path

```
symbol    place_order()                        # Python
  --calls   -> symbol    create_user()          # Python client (snake_case)
  --endpoint-> endpoint  POST /users (orders)    # bound: createUser ≈ create_user
  --boundary-> endpoint  POST /users (users)     # the network hop
  --endpoint-> symbol    CreateUser()           # Go handler (PascalCase), bound the same way
  --calls   -> symbol    SaveUser()             # Go store
```

`create_user()` (Python) and `CreateUser()` (Go) both bound to the one operationId `createUser`,
so the path runs straight through the language boundary as well as the network one.

The `.go`/`.py` files are illustrative — `graph.json` stands in for what graphify would extract from
them, so the walkthrough runs without an extractor installed. See
[docs/cross-service.md](../../docs/cross-service.md).
