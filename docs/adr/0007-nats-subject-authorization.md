# ADR 0007: NATS Subject Authorization

## Status

Accepted (amends [ADR 0001](0001-data-plane-reliability.md), extends
[ADR 0006](0006-validated-data-contract.md))

> ADR 0006 was authored on the `docs/validated-data-contract` branch and may not
> yet be on `main`. This record depends on its Option A fan-out path, which is
> the reason the `fanout` role exists.

## Context

The embedded NATS server ran with no authentication of any kind. `cmd/core/main.go`
set only `Port`, `HTTPPort`, `JetStream` and `StoreDir` on `server.Options`;
`Users`, `Authorization`, `Accounts` and `NoAuthUser` were all zero, and an unset
`Host` means nats-server binds `0.0.0.0` (`server/const.go` `DEFAULT_HOST`).

Anyone who could reach `:4222` could therefore:

1. publish `platform.meta.asset.delete` and destroy master data;
2. publish `platform.data.validated` directly, injecting forged telemetry that
   bypassed validation, enrichment and `unknown_asset_policy` — the built-in VM
   sink writes it to VictoriaMetrics unquestioningly;
3. publish `platform.meta.asset.changed` and poison the enrichment cache, which
   subscribes to that subject;
4. delete or purge the `PLATFORM_DATA` stream;
5. flood `platform.data.>` until the 1 GiB stream limit evicted real data;
6. read the entire validated stream, and profile the deployment through the
   equally unauthenticated monitoring port (`/varz`, `/connz?subs=1`, `/jsz`).

Phase 3 (#101) had just made the *same* master-data mutations require a bearer
token over HTTP (`internal/httpapi/server.go`). The NATS path was an
unauthenticated duplicate of that surface, so the HTTP hardening was
decorative.

[ADR 0003](0003-relationship-based-control.md) already assumes this work:
"the initial mechanism should use NATS credentials and subject permissions".
The control plane cannot be built on an open port.

## Decision

Install a **single account (`$G`) with `server.Options.Users` and per-user
`Permissions`**, and separate *authorization* from *authentication* so the hole
closes without a flag day.

### Modes

| Mode | `Users` | Anonymous connections | Purpose |
|---|---|---|---|
| `compat` (default) | defined | accepted as `legacy` | This release. Authorization enforced, credentials not yet required. |
| `strict` | defined | rejected | Next release's default. |
| `off` | absent | accepted, unrestricted | Deprecated escape hatch. **Refused on a non-loopback `nats.host`.** |

Authorization is on from this release in every mode but `off`. `NoAuthUser`
buys one release of migration time for *authentication* only.

This is not the usual "secure default vs. broken deployments" trade-off,
because a NATS permission violation is **transient**: the server returns `-ERR`
and keeps the connection open. A pre-auth adapter in `compat` mode keeps
running and keeps publishing what it is allowed to publish; only the forbidden
subject is refused, loudly, on the client's async error channel.

### Roles

| Role | Identity | Purpose |
|---|---|---|
| `core` | ephemeral, per process | edg-core itself. `Permissions: nil` (allow-all). Never written to disk; used over `nats.InProcessServer`, so it never crosses a socket. |
| `operator` | credentials file | Master-data writes. Also holds `$JS.API.>` — see "Known limitations". |
| `adapter` | credentials file | Telemetry and alarms in, master-data reads. **Cannot mutate master data.** |
| `fanout` | credentials file | A durable JetStream consumer on the data stream, and nothing else. The ADR 0006 Option A path. |
| `legacy` | none (`NoAuthUser`) | `compat` mode anonymous identity. Deliberately `adapter ∪ fanout`. |

`legacy` is the union rather than just `adapter` because a deployment following
ADR 0006 today has an anonymous Benthos/Vector consumer attached. Mapping
anonymous connections to `adapter` alone — which is denied `$JS.API.>` — would
disconnect it on upgrade, breaking the very promise `compat` exists to make.

### Publish matrix

`●` = allow-all (`Permissions: nil`).

| subject | core | operator | adapter | fanout | legacy |
|---|:--:|:--:|:--:|:--:|:--:|
| `platform.data.asset`, `platform.alarm.raised` | ● | ✅ | ✅ | ⛔ | ✅ |
| `platform.meta.*` reads (get/list/traversal/template/constraints) | ● | ✅ | ✅ | ⛔ | ✅ |
| `platform.meta.{asset,relation}.{create,update,delete}` | ● | ✅ | **⛔** | ⛔ | **⛔** |
| `platform.data.{validated,deadletter}` | ● | ⛔ | ⛔ | ⛔ | ⛔ |
| `platform.meta.*.changed`, `constraints.violation`, `alarm.{grouped,impact.computed}` | ● | ⛔ | ⛔ | ⛔ | ⛔ |
| fanout `$JS.API` subset + `$JS.ACK.>` | ● | ✅ | ⛔ | ✅ | ✅ |
| `$JS.API.STREAM.{DELETE,PURGE,UPDATE}.>`, `CONSUMER.DELETE.>`, `$JS.API.ACCOUNT.>` | ● | ✅ | ⛔ | ⛔ | ⛔ |
| `_INBOX.>` | ● | ⛔ | ⛔ | ✅ | ⛔ |
| `$SYS.>` | ● | ⛔ | ⛔ | ⛔ | ⛔ |

Subscribe: `core`/`operator`/`adapter`/`legacy` get `platform.>` + `_INBOX.>`;
`fanout` gets `platform.data.>` + `_INBOX.>`. `$SYS.>` is denied to all.

Three properties of this table are load-bearing and non-obvious:

- **Allow lists never use wildcards.** `platform.meta.asset.*` would silently
  include `create`/`update`/`delete`. Subjects are enumerated one at a time,
  and the dangerous ones additionally appear in `Deny`. Deny wins over allow, so
  widening an allow list later cannot re-open them by accident.
  `TestDenyWinsOverWildcardAllow` pins this.
- **`_INBOX.>` publish is denied to `operator`/`adapter`/`legacy`, and
  request/reply still works.** The requester only *subscribes* to its inbox; the
  responder (core, allow-all) publishes to it. Denying inbox publishes therefore
  costs nothing and closes response forgery, where an attacker races core to
  answer someone else's request.
- **The `fanout` `$JS.API` set is the verified minimum** for
  `PullSubscribe` → `Fetch` → `Ack`. `STREAM.DELETE`/`PURGE` are not in it, and
  the stream name is interpolated from `jetstream.stream.name` rather than
  hardcoded, so renaming the stream does not silently strip the role.

### Binding

| Setting | Default | Rationale |
|---|---|---|
| `nats.host` | `0.0.0.0` | Unchanged. An edge gateway exists to serve remote adapters and sibling containers. Authorization, not binding, is what closes the hole. |
| `nats.http_host` | **`127.0.0.1`** (changed) | The monitoring port has no authentication mechanism at all. `/connz?subs=1` alone maps the deployment's subject topology. |

### Credentials

Generated on first boot with `crypto/rand`, 32 bytes as `base64.RawURLEncoding`
(43 chars), written atomically at mode `0600`. `EDG_NATS_{OPERATOR,ADAPTER,FANOUT}_PASSWORD`
override the file wholesale, so an orchestrator can inject secrets that never
touch disk; a *partially* set environment is an error rather than a silent
fallback.

Encoding matters: the alphabet excludes `/`, `+`, `=` and `:`, so a secret rides
in `nats://adapter:SECRET@host:4222` with no escaping. **That is the entire SDK
integration** — the SDKs gained no credential parameters, because their existing
URL option already carries them.

### Alternatives considered

| Option | Why not |
|---|---|
| Single token (`Options.Authorization`) | No role separation: the token given to an adapter is also a master-data delete token. Does not solve the problem. |
| NKey (`Options.Nkeys`) | Best posture at rest — the server stores only public keys. But `nats-py` needs the `nats-py[nkeys]` extra, forcing a new runtime dependency on every Python adapter, and a `.nk` seed cannot ride in a URL, so **both SDKs would need public API changes**. Rejected against the "any language's NATS client can participate" constraint. The role table is isolated behind one function, so NKeys can be layered on later without redesigning it. |
| Account isolation (`Options.Accounts`) | Real subject-space and inbox isolation, but core↔adapter traffic then needs stream/service export/import wiring per account, and JetStream becomes per-account. Substantial complexity for a threat that per-user permissions already close on a single-node gateway. Revisit for multi-tenant edge. |
| JWT / operator mode | Needs `nsc` or an in-process resolver. Contradicts "no external services". |
| bcrypt-hashed passwords in `Options.Users` | nats-server supports it, and two of three design proposals adopted it — then both conceded that bcrypt adds nothing to a 256-bit random secret. The cost is a direct dependency on `golang.org/x/crypto` plus ~44 ms per connection. Rejected: the entropy is already the security. |

## Consequences

- `go.mod` is unchanged: `server.User` and `server.Permissions` come from
  nats-server, already a direct dependency, and everything else is stdlib.
  `THIRD_PARTY_LICENSES.md` needs no edit.
- **The ADR 0001 boundary becomes enforceable rather than merely stated.**
  `platform.data.validated` means "accepted by core" because now only core can
  publish it.
- Adapters can no longer create assets over NATS. This is the wire-level
  expression of #100's decision that master data is declared explicitly rather
  than being a side effect of data flow; the HTTP write API (#101) is the
  supported path. No shipped adapter or example called those methods.
- `internal/natsauth` does not import `internal/core` in production code. The
  subject strings are duplicated so the table is reviewable on its own, and
  `subjects_test.go` (test-only import) asserts every subject `MetaHandler`
  serves is classified — adding one without classifying it fails CI.
- Operators lose remote access to `:8222` by default and must set
  `nats.http_host` back to `0.0.0.0` to restore it. The container healthcheck
  is unaffected (it calls loopback from inside the container).

### Known limitations

- **`operator` holds `$JS.API.>` in full**, so master-data administration and
  stream destruction are the same credential. Deliberate for now; tracked in
  #109.
- **`fanout` cannot delete its own consumer.** `$JS.API.CONSUMER.DELETE` is not
  scoped to the caller's consumer, so granting it would also let a fan-out
  delete `edg-core-vm-sink`'s durable consumer and reset the built-in sink's ack
  position (ADR 0005). Consumer lifecycle is an operator task.
- **A denied subscription cannot be reported synchronously.** `nc.Subscribe` is
  fire-and-forget and the server's `-ERR` arrives through nats.go's asynchronous
  callback queue, which is not ordered against `Flush`;
  `nats.PermissionErrOnSubscribe` covers only `SubscribeSync`. Since the symptom
  is otherwise invisible — the adapter simply never receives anything — the SDKs
  log it distinctly instead.
- **No TLS.** Credentials cross the wire in the CONNECT frame. On a loopback or
  trusted-LAN edge deployment this is the same exposure the data already has;
  a routable deployment wants TLS, which is a separate decision.
- **No per-device identity or rotation.** All adapters share one `adapter`
  credential, so revoking one revokes all. Labelled per-device users
  (`adapter.line3`) and `Server.ReloadOptions`-based rotation were designed and
  deferred; the role table is reused unchanged when they land, so this is
  sequencing, not a fork in the design.

### Migration

`compat` is the default and requires no action. Deployments that scrape
`:8222` remotely must set `nats.http_host`. When `strict` becomes the default,
clients need `nats://<role>:<secret>@host:4222`; `edg-core` prints the
credentials path on boot. Anything still publishing to a forbidden subject
appears in the core's log as `Publish Violation - Subject "..."`, which is the
migration checklist for clients that do not use an EDG SDK.

## Validation

- Full role matrix asserted against a live in-process server, per role and per
  subject, including that a violation does not drop the connection.
- Deny-beats-wildcard-allow pinned directly, since the narrow allow lists would
  otherwise mask whether the defensive `Deny` entries work.
- ADR 0006 lock test: `fanout` and `legacy` each create a durable pull consumer,
  fetch and ack; `fanout` is refused `DeleteStream`, `PurgeStream` and
  `DeleteConsumer`, and `adapter` is refused the JetStream API entirely.
- `compat` accepts anonymous connections; `strict` rejects them.
- Credentials: generation, idempotence across boots, environment precedence,
  partial-environment failure, truncated and malformed files, `0600` mode, and
  that the core secret never appears in the file.
- Config: mode enumeration, and that `mode: off` is refused on a non-loopback
  host (including the unset-host case, which means `0.0.0.0`).
- Matrix corrosion: every subject `MetaHandler` serves is classified; no stale
  entries remain; every core-published subject is denied to other roles.
- End-to-end against a running `edg-core`: anonymous, `adapter`, `operator` and
  wrong-password clients probed for publish rights, request/reply liveness and
  connection survival.
