# Unified Master-Data Control — Design Spec

- **Date:** 2026-06-18
- **Status:** Draft (for review)
- **Topic:** Simplify and unify how EDG master data (assets, relations, templates) is managed.
- **Related:** [ADR 0001](../../adr/0001-data-plane-reliability.md), [ADR 0002](../../adr/0002-ontology-enrichment.md), [ADR 0006](../../adr/0006-validated-data-contract.md)

## 1. Goal

Make EDG **master-data management** (기준정보 관리) simpler and more usable. Today the
write surface is fragmented and awkward; this design unifies it behind a single
in-process service that every interface shares, keeps SQLite as the single live
source of truth, makes templates first-class DB citizens, and adds a small
embedded UI so humans can see and edit master data without building external
tooling.

This is a usability/consolidation effort, **not** a feature-expansion effort. We
remove dead config, collapse scattered write paths to one core, and add the two
human-facing affordances that were missing (write API + UI).

## 2. Background — why this shape

A series of design decisions led here (recorded so reviewers see the reasoning):

- **SQLite stays the single live source of truth.** A file-first ("declarative
  bundle") model was considered for its git-reviewability, but it forces an
  `apply` step on every change and creates a dual-writer hazard when both humans
  and automation mutate. SQLite gives transactional, concurrent, immediate-
  consistency writes with no `apply` ceremony. The one thing files gave us for
  free — human inspectability when there is no UI — is instead solved by adding a
  small UI.
- **Templates become DB-authoritative.** Today templates live only as YAML files
  loaded into memory; assets/relations live in SQLite. That is two sources of
  truth. We unify everything into SQLite. Files remain a convenient *import* and
  *export* format (round-trippable YAML), not a live authority.
- **Auto-registration is removed.** Master data must be declared explicitly;
  unknown `asset_id` telemetry no longer silently creates asset records.
- **MCP is explicitly out of scope** for this spec (deferred). Because every
  interface goes through one extracted `MetadataService`, an MCP server can later
  wrap that service (or the HTTP write API) with near-zero additional design.

## 3. Scope

**In scope (phased):**

1. Extract a transport-agnostic **`MetadataService`** + typed error model.
2. **Remove auto-registration**; define explicit unknown-asset policy.
3. **HTTP write API** (asset/relation/template CRUD + import/export) + auth hardening.
4. **Templates → DB** (migration, store methods, loader backend swap, hot-path cache, import/export, bootstrap seed).
5. **Embedded read+CRUD UI** + live refresh.

**Out of scope (future):**

- **MCP server** for agent control (wraps the same service later).
- Reviving `ValidateAssetData` / ingest-time data validation (belongs to the ADR 0006 data-contract track).
- Transactionalizing constraint enforcement (`BeginTx`); current insert→check→rollback is preserved.
- Per-actor auth / audit identity beyond a single bearer token + `source` tagging.

## 4. Current state (as-is, grounded)

| Concern | Today | Reference |
|---|---|---|
| Master-data storage | assets + relations in SQLite; **templates in memory only** (file-loaded) | `migrations/0001..0003`, `loader.go:13-16` |
| Write path | **NATS request/reply only** (`platform.meta.*`); HTTP API is **read-only** | `meta_handler.go:14-26`, `httpapi/server.go:88-100` |
| Business rules | UUID/timestamp, name-dup check, template-exists check, relation-type validity, **constraint enforce/rollback**, event publish — all live **inside NATS-coupled `MetaHandler` handlers** (`func(msg *nats.Msg)`) | `meta_handler.go:159,270,340,523,693` |
| Store layer | Pure SQL CRUD only, **no business rules** | `store.go:200,294,318,335,392,571` |
| Auto-registration | Unknown `asset_id` → auto-create `Source=auto` asset (mode `auto`) | `handler.go:105-131`, `config.go:66-68` |
| Constraint check | Inline, synchronous on every relation create; reads `loader.Get` (hot path) | `meta_handler.go:565` → `constraints.go:51` |
| `auto_reload` | Declared in config, **never read** (dead field; no watcher) | `config.go:52,127` |
| `ValidateAssetData` | Defined but **test-only dead code** | `loader.go:109-148` |
| Auth | Single bearer token; **empty token ⇒ no auth**; no verb distinction | `httpapi/server.go:257-269` |
| Change events | Every mutation publishes `platform.meta.*.changed` (`MetaChangeEvent`) | `events.go:12-13,44-53` |
| Static assets | None served; `go:embed` precedent exists for migrations | `migrate.go:16`, go `1.24.0` |

**Linchpin finding:** because business rules are trapped in NATS-coupled handlers,
any new write interface (HTTP/CLI) that calls `Store` directly **bypasses all
validation**. The read-only HTTP API already bypasses `MetaHandler` and hits
`Store` directly — repeating that on the write side would corrupt master data.
Extracting a shared service is therefore the prerequisite for everything else.

## 5. Target design

### 5.1 Architecture — one store, one service, many interfaces

```
   Embedded UI ─┐     CLI ─┐     (existing) NATS ─┐
                └─────┬────┴── HTTP write API ─────┘
                      │            │
                 ┌────┴────────────┴────┐
                 │   MetadataService    │  ← single place for all business rules
                 │  (transport-agnostic)│     (validation, constraints, events)
                 └──────────┬───────────┘
                            │
                      ┌─────┴─────┐
                      │  *Store   │  ← SQLite: assets, relations, templates
                      └───────────┘
```

Every interface is a thin adapter over `MetadataService`. There is exactly one
mutation path, so validation and change events are identical regardless of who
made the change.

### 5.2 `MetadataService` + error model (Phase 1 — prerequisite)

- New `internal/core/meta_service.go` holding `MetadataService{store, loader, constraints, events, enforcement}`.
- Extract the transport-agnostic logic out of `MetaHandler`'s `func(msg *nats.Msg)`
  handlers into plain methods:
  - `CreateAsset(ctx, CreateAssetRequest) (*Asset, error)`
  - `UpdateAsset(ctx, UpdateAssetRequest) (*Asset, error)`
  - `DeleteAsset(ctx, id, source string) error`
  - `CreateRelation(ctx, CreateRelationRequest) (*AssetRelation, error)`
  - `DeleteRelation(ctx, id, source string) error`
  - (Phase 4) `ImportTemplates(...)`, `ExportTemplates(...)`, template CRUD.
- Preserve exact existing behavior: UUID/timestamp injection, name-duplicate
  check (`GetAssetByName`), template-exists check, `relation_type` validity,
  constraint enforce + compensating-delete rollback, change-event publish.
- The existing NATS handlers become thin wrappers that decode the message, call
  the service, and marshal the `Response{Success,Data,Error}` envelope — keeping
  the existing NATS contract intact.
- **Typed/sentinel errors** (`ErrNotFound`, `ErrConflict`, `ErrValidation`,
  `ErrConstraint`) so HTTP can map to 404/409/400/422 and every interface
  classifies failures from one source. (Today errors are bare `fmt.Errorf`
  strings — not classifiable.)

### 5.3 Remove auto-registration + unknown-asset policy (Phase 2)

- Delete the auto-registration branch (`handler.go:105-131`) and the entire
  `RegistrationMode` surface: `DataHandler.registrationMode`, the config struct
  `AssetRegistrationConfig`, constants, defaults, and the `validate()` switch.
- After removal, telemetry for an undeclared `asset_id` naturally becomes
  **un-enriched pass-through** (enricher returns zero tags for an unknown asset).
- Make this explicit with a new top-level config option that replaces the removed
  `asset_registration` block:

  ```yaml
  # replaces the removed `asset_registration:` block
  unknown_asset_policy: pass_through   # pass_through (default) | dead_letter
  ```

  - `pass_through` (default): publish un-enriched to `platform.data.validated`
    (preserves current `manual`-mode behavior; no silent loss).
  - `dead_letter`: route to the existing dead-letter subject/metrics
    (`handler.go:171`).
  - Add an expvar counter for undeclared-asset hits for operator visibility.
- Migration note + a one-time startup warning if a legacy `asset_registration.mode`
  key is still present in a user's YAML (it is silently ignored by the parser).

### 5.4 HTTP write API + auth (Phase 3)

- Add routes (new `internal/httpapi/write.go`):
  - `POST /api/v1/assets`, `PUT /api/v1/assets/{id}`, `DELETE /api/v1/assets/{id}`
  - `POST /api/v1/relations`, `DELETE /api/v1/relations/{id}`
  - (Phase 4) Template CRUD: `GET /api/v1/templates`, `GET|PUT|DELETE /api/v1/templates/{name}`, `POST /api/v1/templates/import`, `GET /api/v1/templates/export`
  - `GET /api/v1/constraints` (currently CLI-only) for the UI's violations view
- Handlers decode existing request structs (`CreateAssetRequest`, etc.) and call
  `MetadataService`. **Never call `Store` directly** (would bypass validation).
- Status mapping from sentinel errors: 400 / 404 / 409 / 422 / 500.
- Extend `NewServer` to receive the service (+ `EventPublisher`/NATS for live
  refresh). Today it only holds `*core.Store`.
- Extend CORS `Allow-Methods` to include POST/PUT/DELETE.
- **Auth hardening:** when write routes are enabled, require a non-empty bearer
  token (no more anonymous pass-through). Record provenance via the request
  `source` field on change events. (Session/cookie + CSRF is a future option;
  same-origin embedded UI with a bearer header is acceptable for the PoC.)

### 5.5 Templates → DB (Phase 4)

- **Migration `0004_templates`** (follow existing `embed.FS` + golang-migrate pattern):
  - `templates(name TEXT PRIMARY KEY, created_at, updated_at)`
  - `template_resources(template_name REFERENCES templates(name) ON DELETE CASCADE, name, value_type, unit, PRIMARY KEY(template_name,name))`
  - `template_constraints(id PK, template_name REFERENCES templates(name) ON DELETE CASCADE, kind CHECK(kind IN ('required','forbidden')), relation_type, target_template, min_count NULL, max_count NULL)`
  - `PRAGMA foreign_keys=ON` is already set (`store.go:41`).
- **Store methods** (new `store_templates.go`): `UpsertTemplate` (transactional
  replace of the 3 tables), `GetTemplate`, `ListTemplates`, `TemplateExists`,
  `DeleteTemplate`.
- **`TemplateLoader` backend swap:** keep its `Get/List/Exists/Count` signatures
  but delegate to `Store`, so `MetaHandler`/`ConstraintsEvaluator` call sites are
  unchanged.
- **Hot-path cache:** the constraint check runs on every relation create
  (`loader.Get`). Keep an in-memory template cache loaded at boot and invalidated
  on template writes via a new `SubjectTemplateChanged` event — do not turn each
  relation create into a DB read.
- **`import`** (`template_import.go`): parse YAML (reuse current unmarshal) and
  upsert into DB. Directory import = single transaction + a final whole-set
  cross-reference validation (`target_template` existence). Single-template write
  policy for a not-yet-existing `target_template`: reject by default.
- **`export`** (`template_export.go`): DB → YAML, one file per template (matching
  the current `templates/*.yaml` convention) so export output re-imports cleanly
  (round-trip).
- **Bootstrap seed:** remove the boot-time auto `LoadFromDir`; on an empty DB,
  run a one-time seed import from the configured templates path so existing
  deployments don't start with zero templates.
- **Cleanup:** remove the dead `auto_reload` field; repurpose/remove
  `Templates.Dir` as the import/seed path.
- `assets.template_name` stays a **loose text reference** (no FK) to remain
  compatible with existing data; validity is checked at write time.

### 5.6 Embedded UI + live refresh (Phase 5)

- Static SPA embedded with `go:embed` (new `internal/webui/`), served at `/` by
  the existing HTTP server with SPA fallback; `/api/` prefix returns explicit JSON
  404s (no HTML fallback for API paths).
- **Minimal scope:** list/inspect assets, relations, templates; basic CRUD forms;
  template import button; constraint-violations view; hierarchy shown as a **text
  tree** (from the `subtree` API) — node-edge graph visualization is deferred.
- **Build:** commit the built `dist/` and embed it (zero frontend toolchain in the
  Go build — best fits the single-binary ethos). Vanilla JS/HTML, no bundler.
  Guard `go:embed` against an empty `dist/` (placeholder/build tag) so backend
  builds never break.
- **Live refresh:** the server subscribes to `platform.meta.*.changed`
  (the pattern `enricher.go` already uses) and bridges to the browser via SSE.
- `webui_enabled` config toggle (default on when HTTP is enabled) for headless
  deployments.

## 6. Decisions & defaults

These are baked into this spec; flag any you want changed during review.

| Decision | Default |
|---|---|
| `MetadataService` location | `internal/core/meta_service.go`; NATS handlers become thin wrappers |
| Existing NATS write subjects | **Kept** (route through the service) |
| Unknown-asset data policy | `pass_through` default, `dead_letter` opt-in (reject rejected — silent loss) |
| CLI mutations | Delegate to the daemon via HTTP/NATS (avoid concurrent direct SQLite writes / "database is locked") |
| Constraint enforce | Keep insert→check→compensating-delete; transactional `BeginTx` deferred |
| `assets.template_name` | Loose text reference (no FK); validated at write time |
| Template import unit | Directory = one transaction + whole-set validation; single write rejects unknown `target_template` |
| Template export format | One file per template (round-trips with import) |
| UI build | Committed `dist/` + vanilla JS, embedded; `webui_enabled` toggle |
| Write auth | Require non-empty bearer token when write enabled |
| `ValidateAssetData` / ingest validation | Out of scope (ADR 0006 track) |

## 7. Phased implementation plan

| Phase | Deliverable | Depends on |
|---|---|---|
| **1** | `MetadataService` extraction + sentinel errors; NATS handlers → wrappers (no behavior change) | — |
| **2** | Remove auto-registration + `RegistrationMode`; add `unknown_asset_policy` + counter | 1 |
| **3** | HTTP write endpoints (asset/relation) + auth hardening + CORS | 1 (parallel with 2) |
| **4** | Templates → DB: migration 0004, store methods, loader backend swap, hot-path cache, import/export, seed; template HTTP endpoints | 1 |
| **5** | Embedded read+CRUD UI + SSE live refresh + `GET /constraints` | 3 |

Phase 1 is the gate for everything. Phases 2 and 3 can run in parallel; Phase 4
is independent after Phase 1; Phase 5 (UI) builds on Phase 3. MCP is out of scope
(no phase).

## 8. Risks & mitigations

| Risk | Mitigation |
|---|---|
| **Bypassing the service** corrupts data (rules live in handlers, not `Store`) | Phase 1 first; HTTP/CLI must call the service, never `Store` directly; regression tests on the NATS contract |
| **SQLite single-writer** contention (multiple interfaces/processes) | CLI/UI delegate to the daemon; one process owns the DB; no separate process opens the same `metadata.db` |
| Constraint enforce is non-transactional (insert→check→rollback, error ignored) | Preserve current behavior now; note `BeginTx` as a future hardening; cover with tests |
| Auth gap (empty token ⇒ anonymous) becomes dangerous with writes | Require non-empty token when write enabled; tag provenance via `source` |
| Removing boot-time template load ⇒ empty-DB deployments start with no templates | One-time seed import on empty DB |
| Auto-reg removal ⇒ undeclared sensor data loses enrichment silently | Explicit `unknown_asset_policy` + visibility counter; `pass_through` default avoids loss |
| Pre-existing build issues (`release.yml` go 1.21 vs go.mod 1.24; CGO for go-sqlite3) | Out of band; embedded UI is pure Go and does not worsen it; fix separately |

## 9. Testing strategy

- **Phase 1:** characterization/regression tests proving the NATS contract
  (`Response` envelope, validation, events) is unchanged after extraction.
- **Phase 2:** undeclared-asset `pass_through` and `dead_letter` paths; legacy
  config-key warning.
- **Phase 3:** HTTP write happy paths + status-code mapping (400/404/409/422);
  auth enforcement.
- **Phase 4:** template round-trip (`import → export → import` equality),
  migration up/down (`tableExists`), hot-path cache invalidation on template write.
- **Phase 6:** UI served + SPA fallback vs `/api` 404; SSE emits on mutation.

## 10. Open questions for review

1. Spec/ADR home: keep this detailed spec here, and/or distill a repo-native
   **ADR 0007** summarizing the decision?
2. Commit target: this is on branch `docs/validated-data-contract`; should the
   spec (and later work) live on a new branch?
3. Any default in §6 to override before we write the implementation plan?
