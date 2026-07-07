# go-multitenant-gateway

A small, single-binary multi-tenant API gateway in Go. One process serves many
tenants and many product modules, behind one middleware chain: recover → tenant
resolution → per-tenant rate limit → auth → module.

This is a reference implementation. It mirrors, in miniature, a gateway I run in
production that serves a fleet of apps from one binary — the real one has many
more modules and routes. This repo keeps the pattern and drops the product code,
so it stays small enough to read in one sitting.

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

## Layout

```
cmd/gateway            entrypoint — loads config, builds the app, listens
internal/server        wires the middleware chain + modules (tested here)
internal/middleware    tenant resolution · per-tenant rate limit · JWT auth
internal/modules       the Module interface + example modules (health, notes)
internal/tenant        tenant identity carried on the request context
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

## What this reference simplifies (and what production does)

- **Auth:** here, HS256 with a shared secret, so it runs with one env var. In
  production the gateway verifies RS256 tokens against the auth service's public
  keys (JWKS), so it holds no signing material and keys can rotate independently.
- **Rate limit:** here, in-memory, fine for one instance. With multiple replicas
  the counter lives in Redis so replicas share one budget.
- **Data:** handlers read the tenant's Postgres schema from the context. This
  repo has no database wired in; the point is the isolation boundary, not the
  driver.

Each of these is called out at the point in the code where it matters.

## Tests

```bash
go test ./...
```

The middleware chain is covered end-to-end in `internal/server`: health without
auth, tenant required, token required, valid request passes, tampered token
rejected.

## License

MIT © Yusuf İhsan Görgel
