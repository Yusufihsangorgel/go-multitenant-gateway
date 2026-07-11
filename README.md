# go-multitenant-gateway

A small, single-binary multi-tenant API gateway in Go. One process serves many
tenants and many product modules, behind one middleware chain: recover → tenant
resolution → per-tenant rate limit → auth → module.

This is a reference implementation. It mirrors, in miniature, a gateway I run in
production that serves a fleet of apps from one binary — the real one has many
more modules and routes. This repo keeps the pattern and drops the product code,
so it stays small enough to read in one sitting.


> **Background:** I wrote up the design decisions behind this — the case for and against one binary for many products — [on my blog](https://yusufihsangorgel.github.io/2026/07/07/one-go-binary-for-and-against.html).

## Architecture

Every request runs the same chain. `recover` wraps everything; health is mounted
*before* the chain so probes need no tenant or token; every other route resolves
a tenant, spends against that tenant's rate budget, and passes auth before a
module handler ever runs.

```mermaid
---
config:
  look: handDrawn
---
flowchart TB
    REQ(["Incoming HTTP request"]) --> REC["recover<br/>catch panics &rarr; 500"]
    REC --> HEALTH{"path is /health/* ?"}
    HEALTH -->|"yes (mounted before the chain)"| HZ["health module &rarr; 200, no auth"]
    HEALTH -->|no| T["Tenant middleware<br/>X-Tenant header &rarr; ctx, 400 if missing"]
    T --> RL["RateLimit middleware<br/>per-tenant fixed window, 429 if over"]
    RL --> AUTH["Auth middleware<br/>verify Bearer JWT &rarr; userID, 401 if invalid"]
    AUTH --> ROUTER["Module router<br/>match path prefix"]
    ROUTER --> H["Module handler<br/>reads tenant + user from ctx"]
```

One binary hosts every product module behind that chain. A module owns a path
prefix and is listed once; adding a product does not add a service. All tenants
share one Postgres, each isolated in its own schema — resolved at the edge and
carried on the context, so a handler never picks a schema from raw input.

```mermaid
---
config:
  look: handDrawn
---
flowchart LR
    subgraph BIN["Single binary — one deploy, one log stream"]
      direction TB
      CHAIN["shared middleware chain<br/>tenant &middot; rate limit &middot; auth"]
      CHAIN --> M1["health module"]
      CHAIN --> M2["notes module"]
      CHAIN --> M3["… ~17 modules<br/>in the production fleet"]
    end
    M2 --> PG
    M3 --> PG
    subgraph PG["One shared Postgres — schema per tenant"]
      direction TB
      SA["tenant_acme"]
      SB["tenant_globex"]
      SC["tenant_…"]
    end
```

The tradeoffs behind these choices are written up in
[ARCHITECTURE.md](ARCHITECTURE.md).

## Why single-binary, multi-tenant?

For a small team (or one operator), the dominant cost is not compute — it is the
number of things that can break. One binary means one deploy, one log stream,
one place to put cross-cutting middleware. Adding a product is adding a module
and listing it once; no new service, no new deploy. The marginal cost of a new
product is close to zero.

The tradeoffs that come with that choice — a shared failure domain,
schema-per-tenant instead of database-per-tenant — are written up in
[ARCHITECTURE.md](ARCHITECTURE.md). They are real, and pretending they are not is
how you get bitten.

## Run it

```bash
go run ./cmd/gateway        # listens on :8080
```

```bash
# health needs no auth
curl localhost:8080/health/

# every other route is scoped to a tenant and needs a bearer token
curl -H "X-Tenant: acme" -H "Authorization: Bearer <jwt>" localhost:8080/notes/
```

A request with no tenant is `400`; with a tenant but no/invalid token, `401`.

## Run it with Postgres and Redis

By default everything above runs in memory, so the quickstart needs no
services. Set `DATABASE_URL` and/or `REDIS_URL` to switch on the production
wiring: notes live schema-per-tenant in Postgres, and the rate limiter counts
in Redis so several replicas spend from one budget.

```bash
docker compose up -d

DATABASE_URL='postgres://postgres:postgres@localhost:5432/gateway?sslmode=disable' \
REDIS_URL='redis://localhost:6379/0' \
SEED_TENANTS=acme,globex \
go run ./cmd/gateway
```

At boot the gateway registers every tenant in `SEED_TENANTS`, creates their
schemas and runs the embedded per-schema migrations for everything in the
registry. Tenants exist only through that registry (seeded here, or via
`migrate.EnsureTenant`). In this mode the tenant middleware resolves each
request's schema *from* the registry (with a short-lived cache in front), so a
request for an unregistered tenant is rejected with `404` before any SQL runs,
and no request input ever reaches SQL as an identifier.

## Layout

```
cmd/gateway            entrypoint — loads config, builds the app, listens
internal/server        wires the middleware chain + modules (tested here)
internal/middleware    tenant resolution · per-tenant rate limit · JWT auth
internal/modules       the Module interface + example modules (health, notes)
internal/tenant        tenant identity carried on the request context
internal/db            pgx pool + the transaction primitive that pins search_path to one tenant schema
internal/migrate       per-schema migration runner over embedded SQL, with a ledger in each schema
internal/config        env-driven config
```

A **module** is one product's slice of the gateway: a path prefix and its
routes. That is the extension point — see `internal/modules/notes` for the
shape a real one takes.

## Config

| Env | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | listen port |
| `JWT_SECRET` | `dev-secret-change-me` | HS256 secret (see note below) |
| `RATE_PER_MINUTE` | `120` | per-tenant request budget |
| `DATABASE_URL` | *(empty)* | Postgres DSN; empty keeps the in-memory notes store |
| `REDIS_URL` | *(empty)* | Redis URL; empty keeps the in-memory rate limiter |
| `DB_MAX_CONNS` | `8` | pgx pool cap |
| `SEED_TENANTS` | *(empty)* | comma-separated tenant IDs registered and migrated at boot |

## What this reference simplifies (and what production does)

- **Auth:** here, HS256 with a shared secret, so it runs with one env var. In
  production the gateway verifies RS256 tokens against the auth service's public
  keys (JWKS), so it holds no signing material and keys can rotate independently.
  This is the one piece this repo still simplifies.
- **Rate limit:** in-memory by default, fine for one instance. Set `REDIS_URL`
  and the counter moves to Redis (fixed window, pipelined `INCR` + `EXPIRE NX`),
  so every replica spends from one budget. On a store error the limiter fails
  open: availability over strict limiting, the tradeoff ARCHITECTURE.md
  describes. Both limiters ship in `internal/middleware`.
- **Data:** in-memory by default. Set `DATABASE_URL` and the notes module runs
  against Postgres, schema per tenant: the schema itself comes from the tenants
  registry rather than the raw header, every query goes through a transaction
  whose `search_path` is pinned to exactly the tenant's schema (`internal/db`),
  and a hand-rolled runner (`internal/migrate`) migrates each schema with its
  own ledger, so tenants can be created and migrated independently — including
  by several replicas at once (per-schema advisory lock).

Each of these is called out at the point in the code where it matters.

## Tests

```bash
go test ./...
```

Unit tests run with no services: the middleware chain end-to-end in
`internal/server` (health without auth, tenant required, token required, valid
request passes, tampered token rejected), identifier sanitization, the
registry cache, migration loading and ordering, the fixed-window limiter
against a fake counter, and the notes module on its in-memory store.

Integration tests are gated on env and skip when it is absent:

```bash
docker compose up -d

TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/gateway?sslmode=disable' \
TEST_REDIS_URL='redis://localhost:6379/0' \
go test -race ./...
```

They prove the claims that matter: two schemas answering the same unqualified
query with different rows, `search_path` never leaking across pooled
connections under concurrency, migration idempotence, concurrent runners on
the same schema (the rolling-deploy case), halfway-failure recovery, the full
app rejecting unregistered tenants before any SQL, and the Redis limiter
sharing one budget across two app instances and resetting after the window.
CI runs the full set against postgres:16 and redis:7 service containers.

## License

MIT © Yusuf İhsan Görgel
