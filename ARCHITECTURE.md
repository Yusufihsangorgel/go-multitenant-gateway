# Architecture & tradeoffs

This gateway is built around one constraint: **many products, one operator.**
Every decision below follows from that. None of them is universally right; they
are right for the constraint.

## One binary vs. a service per product

Chose one. For a small team the dominant cost is operational surface — the
number of independent things that can fail, deploy, and page you. One binary
means one deploy artifact, one log stream, one middleware chain.

**The cost, stated honestly:** a shared failure domain. A bad deploy takes every
product down at once, and the process stays modular only by discipline, not by
network boundaries. Mitigations: the artifact is a single binary so rollback is
instant; health checks gate the routes. The day one product needs its own
release cadence or blast-radius isolation is the day it graduates to its own
service. Until then, 17 services for one operator is simply unoperable.

## Schema-per-tenant vs. database-per-tenant

Chose schema-per-tenant: all tenants share one Postgres database, each in its
own schema. One connection pool, one backup job, one migration runner, one
thing to tune.

**The cost:** blast radius. A runaway query or a bad migration can affect
neighbors, and you cannot move one tenant to its own hardware without work.
Mitigation: the tenant is resolved once at the edge and carried on the context;
handlers never choose a schema from raw input, and the schema itself comes from
a registry lookup, so a header can't point a query at an arbitrary schema —
only at a tenant somebody deliberately registered. A tenant that outgrows the shared database graduates to
its own — the schema boundary is the seam you cut along.

## Auth verified at the edge, via public keys

The gateway verifies JWTs; it does not issue them. In production it checks RS256
signatures against the auth service's public keys (JWKS), so the gateway holds
no signing material. Keys can rotate, and auth can redeploy, without touching
the gateway. The verification happens once, in middleware, before any handler
runs: auth is a gateway concern, not a per-handler one.

This is the one piece the reference in this repo deliberately simplifies. The
committed `internal/middleware/auth.go` verifies an HS256 shared secret so the
gateway runs from a single env var. The chain position and the verify-once-at-
the-edge principle are identical to the JWKS design above; only the key
mechanism differs.

**The cost:** a JWKS fetch/cache path and clock-skew handling. Cheap, and worth
it to keep signing keys in exactly one place.

## Rate limiting in a shared store

Because the gateway runs several replicas, an in-process limiter would let each
replica grant the full budget. The counter belongs in a shared store (Redis) so
all replicas see one budget per tenant.

**The cost:** a round-trip on the hot path, kept cheap and set to fail *open* —
if the limiter store is down, requests pass rather than error. Availability over
strict limiting.

## Slow work leaves the request path

Anything slow or retryable — image processing, email, backups, index pings —
does not run inline. It goes to a background worker over a queue, with retries
and a per-product queue namespace so one noisy product cannot starve the others.

**The cost:** eventual consistency in the UX (an email is *queued*, not *sent*)
and one more moving part. Worth it: the request path stays fast and predictable.

(This part is not modeled in this reference — the repo stops at the request
path. It is here because the decision belongs to the same architecture.)

## What I would change first

- **Break out the biggest module.** In the production system one module is far
  larger than the rest; it is the first candidate to become its own service once
  it justifies the operational cost.
- **Make the shared failure domain explicit** — per-module circuit breaking or
  feature flags, so one product's bad path cannot cascade inside the process.
- **Per-tenant databases for the few tenants that need real isolation,** keeping
  schema-per-tenant as the default for the long tail.
