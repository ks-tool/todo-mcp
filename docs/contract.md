# API contract checking

Two services that talk over an API have a contract: the endpoints one offers and the shapes it
carries. `todo contract` checks that the contract still holds between two specs — what the
**consumer** was built against, and what the **provider** now offers.

```
todo contract <consumer-spec> <provider-spec>
```

It prints the endpoints present and compatible in both, and the breaks; it exits `2` when any break
is found, so CI can gate on it. Available over MCP as the `contract` tool.

## What it detects

- **orphan-call** — an endpoint the consumer needs that the provider no longer offers (removed or
  renamed).
- **schema-drift** — an endpoint present in both whose request or response shape diverged (a field
  the consumer relies on that the provider dropped, or a changed shape).

The comparison is a shallow signature: the parameter and body/field names, with one level of `$ref`
resolution — enough to catch a broken contract without a full type check.

## Protocols

The format is chosen by the file, so the same command spans four kinds of API:

| Protocol | Detected by | Endpoint identity | Signature |
|---|---|---|---|
| **OpenAPI** (REST) | `.json`/`.yaml`, or content | `METHOD /path` | params + request/response schema fields |
| **AsyncAPI** (events: Kafka/AMQP/MQTT) | the `asyncapi`/`channels` fields | `PUB`/`SUB` + channel | message payload fields |
| **gRPC** | `.proto` | `Service/Method` | request/response message fields |
| **GraphQL** | `.graphql`/`.graphqls`/`.gql` | `Query`/`Mutation`/`Subscription` + field | arguments + return type's fields |

OpenAPI and AsyncAPI parse from JSON or YAML. The gRPC and GraphQL readers cover the subset a
contract diff needs — services/rpcs/messages, and types/fields — not a full compiler.

## Why in this tool

When two services are both tracked in one backlog, "is the API contract still honoured" becomes one
structured answer — the list of breaks — instead of reading both repositories in full. That is the
boost: less to read, and a deterministic result a CI step or an assistant can act on.

## Example

```
$ todo contract consumer.yaml provider.yaml
matched 1 endpoint(s)
  ok  POST /users

BREAKS (2):
  [orphan-call] GET /legacy — the provider no longer offers this endpoint
  [schema-drift] GET /users/{id} — response fields the provider no longer returns: email
```
