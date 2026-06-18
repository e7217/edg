# MetadataService Extraction (Phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the transport-agnostic `MetadataService` from the NATS-coupled `MetaHandler` so business rules (validation, constraint enforcement, event publishing) live in one place that HTTP / CLI / future MCP can all reuse — with **no behavior change** to the existing NATS contract.

**Architecture:** Today every mutation rule is embedded in `MetaHandler`'s `func(msg *nats.Msg)` handlers (`internal/core/meta_handler.go`). We move that logic into a new `MetadataService` whose methods take request structs and return `(*Asset|*AssetRelation, error)`. The NATS handlers become thin adapters: decode → call service → map error to `Response` → reply. A typed `ServiceError{Kind, Msg}` carries an error category (so later HTTP can map to 4xx) while its `Error()` string stays byte-identical to today's responses (so the NATS contract is preserved).

**Tech Stack:** Go 1.24, SQLite (`mattn/go-sqlite3`), NATS (`nats.go`), `google/uuid`, testify (`require`/`assert`). No new dependencies.

## Global Constraints

- Go version floor: **1.24.0** (`go.mod`).
- **No new third-party dependencies** in this phase (stdlib `errors`/`fmt` only).
- **Preserve the existing NATS contract exactly:** the `Response{Success,Data,Error}` envelope and every `Error` string must stay identical. `internal/core/meta_handler_test.go` is the regression suite and must pass unchanged.
- Single-binary, single-package: all new code lives in package `core` under `internal/core/`.
- Follow existing test patterns: in-memory store via `NewStore(":memory:")`, empty templates via `NewTemplateLoader()`, `testify` assertions.
- This is a pure refactor: **no functional changes, no new endpoints** (those are later phases).

---

## File Structure

- **Create `internal/core/service_error.go`** — typed `ServiceError` + `ErrorKind` + `KindOf` classifier. One responsibility: error categorization shared by all interfaces.
- **Create `internal/core/meta_service.go`** — `MetadataService` struct, constructor, and the five mutation methods (`CreateAsset`, `UpdateAsset`, `DeleteAsset`, `CreateRelation`, `DeleteRelation`) plus the two constraint helpers moved out of `MetaHandler`.
- **Create `internal/core/service_error_test.go`** and **`internal/core/meta_service_test.go`** — unit tests for the above (no NATS needed).
- **Modify `internal/core/meta_handler.go`** — `MetaHandler` gains a `*MetadataService` field; the five mutation handlers delegate to it; the two constraint helpers are removed (moved to the service). Read/traversal handlers are untouched.
- **`internal/core/meta_handler_test.go`** — unchanged; used as the regression gate.

---

### Task 1: Typed `ServiceError` + classifier

**Files:**
- Create: `internal/core/service_error.go`
- Test: `internal/core/service_error_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type ErrorKind int` with consts `ErrInternal, ErrValidation, ErrNotFound, ErrConflict, ErrConstraint` (in that order; `ErrInternal == 0`).
  - `type ServiceError struct { Kind ErrorKind; Msg string }` with `func (e *ServiceError) Error() string`.
  - `func newServiceError(kind ErrorKind, format string, args ...any) *ServiceError`.
  - `func KindOf(err error) ErrorKind` — returns the kind of a `*ServiceError` (via `errors.As`), else `ErrInternal`.

- [ ] **Step 1: Write the failing test**

```go
// internal/core/service_error_test.go
package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceError_MessageAndKind(t *testing.T) {
	err := newServiceError(ErrValidation, "name is required")
	require.Equal(t, "name is required", err.Error())
	require.Equal(t, ErrValidation, KindOf(err))
}

func TestServiceError_Formatting(t *testing.T) {
	err := newServiceError(ErrNotFound, "asset %s not found", "abc")
	require.Equal(t, "asset abc not found", err.Error())
	require.Equal(t, ErrNotFound, KindOf(err))
}

func TestKindOf_NonServiceErrorIsInternal(t *testing.T) {
	require.Equal(t, ErrInternal, KindOf(errors.New("boom")))
	require.Equal(t, ErrInternal, KindOf(nil))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run 'TestServiceError|TestKindOf' -v`
Expected: FAIL — `undefined: newServiceError`, `undefined: ErrValidation`, etc.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/core/service_error.go
package core

import (
	"errors"
	"fmt"
)

// ErrorKind classifies a service-layer error so transports (HTTP status codes,
// MCP error types) can react consistently. The Error() string is unchanged from
// the legacy NATS responses, so the NATS contract is preserved.
type ErrorKind int

const (
	ErrInternal ErrorKind = iota
	ErrValidation
	ErrNotFound
	ErrConflict
	ErrConstraint
)

// ServiceError is a categorized, transport-agnostic error.
type ServiceError struct {
	Kind ErrorKind
	Msg  string
}

func (e *ServiceError) Error() string { return e.Msg }

func newServiceError(kind ErrorKind, format string, args ...any) *ServiceError {
	return &ServiceError{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// KindOf returns the ErrorKind of err if it is (or wraps) a *ServiceError,
// otherwise ErrInternal. KindOf(nil) is ErrInternal.
func KindOf(err error) ErrorKind {
	var se *ServiceError
	if errors.As(err, &se) {
		return se.Kind
	}
	return ErrInternal
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -run 'TestServiceError|TestKindOf' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/service_error.go internal/core/service_error_test.go
git commit -m "feat(core): add typed ServiceError for transport-agnostic errors"
```

---

### Task 2: `MetadataService` + asset mutations

**Files:**
- Create: `internal/core/meta_service.go`
- Test: `internal/core/meta_service_test.go`

**Interfaces:**
- Consumes: `ServiceError`/`newServiceError`/`ErrorKind` (Task 1); existing `Store` methods `GetAssetByName(string) (*Asset, error)`, `GetAsset(string) (*Asset, error)`, `CreateAsset(*Asset) error`, `UpdateAsset(*Asset) error`, `DeleteAsset(string) error`; `TemplateLoader.Exists(string) bool`; `EventPublisher.PublishAssetChanged(MetaChangeEvent)` (nil-safe); request structs `CreateAssetRequest`, `UpdateAssetRequest`, `DeleteAssetRequest` (already defined in `meta_handler.go`).
- Produces:
  - `type MetadataService struct { ... }`
  - `func NewMetadataService(store *Store, loader *TemplateLoader, events *EventPublisher, enforcement string) *MetadataService`
  - `func (s *MetadataService) CreateAsset(req CreateAssetRequest) (*Asset, error)`
  - `func (s *MetadataService) UpdateAsset(req UpdateAssetRequest) (*Asset, error)`
  - `func (s *MetadataService) DeleteAsset(req DeleteAssetRequest) error`

- [ ] **Step 1: Write the failing test**

```go
// internal/core/meta_service_test.go
package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestService(t *testing.T) *MetadataService {
	t.Helper()
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	// nil EventPublisher is safe: publish methods guard on p == nil.
	return NewMetadataService(store, NewTemplateLoader(), nil, ConstraintsEnforcementWarn)
}

func TestService_CreateAsset_Success(t *testing.T) {
	s := newTestService(t)

	asset, err := s.CreateAsset(CreateAssetRequest{Name: "pump-101"})
	require.NoError(t, err)
	require.NotEmpty(t, asset.ID)
	require.Equal(t, "pump-101", asset.Name)

	got, err := s.store.GetAsset(asset.ID)
	require.NoError(t, err)
	require.Equal(t, "pump-101", got.Name)
}

func TestService_CreateAsset_NameRequired(t *testing.T) {
	s := newTestService(t)

	_, err := s.CreateAsset(CreateAssetRequest{})
	require.Error(t, err)
	require.Equal(t, "name is required", err.Error())
	require.Equal(t, ErrValidation, KindOf(err))
}

func TestService_CreateAsset_DuplicateName(t *testing.T) {
	s := newTestService(t)
	_, err := s.CreateAsset(CreateAssetRequest{Name: "dup"})
	require.NoError(t, err)

	_, err = s.CreateAsset(CreateAssetRequest{Name: "dup"})
	require.Error(t, err)
	require.Equal(t, "asset name already exists", err.Error())
	require.Equal(t, ErrConflict, KindOf(err))
}

func TestService_CreateAsset_TemplateNotFound(t *testing.T) {
	s := newTestService(t)

	_, err := s.CreateAsset(CreateAssetRequest{Name: "x", TemplateName: "missing"})
	require.Error(t, err)
	require.Equal(t, "template not found", err.Error())
	require.Equal(t, ErrValidation, KindOf(err))
}

func TestService_UpdateAsset_NotFound(t *testing.T) {
	s := newTestService(t)

	_, err := s.UpdateAsset(UpdateAssetRequest{ID: "nope", Name: "n"})
	require.Error(t, err)
	require.Equal(t, "asset not found", err.Error())
	require.Equal(t, ErrNotFound, KindOf(err))
}

func TestService_UpdateAsset_Success(t *testing.T) {
	s := newTestService(t)
	created, err := s.CreateAsset(CreateAssetRequest{Name: "old"})
	require.NoError(t, err)

	updated, err := s.UpdateAsset(UpdateAssetRequest{ID: created.ID, Name: "new"})
	require.NoError(t, err)
	require.Equal(t, "new", updated.Name)
	require.Equal(t, created.CreatedAt.UnixMilli(), updated.CreatedAt.UnixMilli())
}

func TestService_DeleteAsset_IDRequired(t *testing.T) {
	s := newTestService(t)

	err := s.DeleteAsset(DeleteAssetRequest{})
	require.Error(t, err)
	require.Equal(t, "id is required", err.Error())
	require.Equal(t, ErrValidation, KindOf(err))
}

func TestService_DeleteAsset_Success(t *testing.T) {
	s := newTestService(t)
	created, err := s.CreateAsset(CreateAssetRequest{Name: "gone"})
	require.NoError(t, err)

	require.NoError(t, s.DeleteAsset(DeleteAssetRequest{ID: created.ID}))

	got, err := s.store.GetAsset(created.ID)
	require.NoError(t, err)
	require.Nil(t, got)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestService_ -v`
Expected: FAIL — `undefined: NewMetadataService`, `MetadataService` has no `CreateAsset`, etc.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/core/meta_service.go
package core

import (
	"time"

	"github.com/google/uuid"
)

// MetadataService holds the transport-agnostic master-data mutation rules.
// HTTP, CLI, MCP, and the NATS handlers all call into this one place, so
// validation, constraint enforcement, and event publishing stay consistent.
type MetadataService struct {
	store                 *Store
	loader                *TemplateLoader
	events                *EventPublisher
	constraints           *ConstraintsEvaluator
	constraintEnforcement string
}

func NewMetadataService(store *Store, loader *TemplateLoader, events *EventPublisher, enforcement string) *MetadataService {
	if enforcement == "" {
		enforcement = ConstraintsEnforcementWarn
	}
	return &MetadataService{
		store:                 store,
		loader:                loader,
		events:                events,
		constraints:           NewConstraintsEvaluator(loader),
		constraintEnforcement: enforcement,
	}
}

func (s *MetadataService) CreateAsset(req CreateAssetRequest) (*Asset, error) {
	if req.Name == "" {
		return nil, newServiceError(ErrValidation, "name is required")
	}
	if existing, _ := s.store.GetAssetByName(req.Name); existing != nil {
		return nil, newServiceError(ErrConflict, "asset name already exists")
	}
	if req.TemplateName != "" && !s.loader.Exists(req.TemplateName) {
		return nil, newServiceError(ErrValidation, "template not found")
	}

	asset := &Asset{
		ID:           uuid.New().String(),
		Name:         req.Name,
		TemplateName: req.TemplateName,
		Labels:       req.Labels,
		ExternalIDs:  req.ExternalIDs,
		Source:       req.Source,
		Attributes:   req.Attributes,
		CreatedAt:    time.Now(),
	}
	if err := s.store.CreateAsset(asset); err != nil {
		return nil, err
	}

	s.events.PublishAssetChanged(MetaChangeEvent{
		EventType: EventCreated,
		EntityID:  asset.ID,
		Source:    asset.Source,
		After:     asset,
	})
	return asset, nil
}

func (s *MetadataService) UpdateAsset(req UpdateAssetRequest) (*Asset, error) {
	if req.ID == "" {
		return nil, newServiceError(ErrValidation, "id is required")
	}
	if req.Name == "" {
		return nil, newServiceError(ErrValidation, "name is required")
	}
	if req.TemplateName != "" && !s.loader.Exists(req.TemplateName) {
		return nil, newServiceError(ErrValidation, "template not found")
	}

	existing, err := s.store.GetAsset(req.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, newServiceError(ErrNotFound, "asset not found")
	}

	asset := &Asset{
		ID:           req.ID,
		Name:         req.Name,
		TemplateName: req.TemplateName,
		Labels:       req.Labels,
		ExternalIDs:  req.ExternalIDs,
		Source:       req.Source,
		Attributes:   req.Attributes,
		CreatedAt:    existing.CreatedAt,
	}
	if err := s.store.UpdateAsset(asset); err != nil {
		return nil, err
	}

	updated, err := s.store.GetAsset(req.ID)
	if err != nil {
		return nil, err
	}

	s.events.PublishAssetChanged(MetaChangeEvent{
		EventType: EventUpdated,
		EntityID:  updated.ID,
		Source:    updated.Source,
		Before:    existing,
		After:     updated,
	})
	return updated, nil
}

func (s *MetadataService) DeleteAsset(req DeleteAssetRequest) error {
	if req.ID == "" {
		return newServiceError(ErrValidation, "id is required")
	}

	before, err := s.store.GetAsset(req.ID)
	if err != nil {
		return err
	}
	if err := s.store.DeleteAsset(req.ID); err != nil {
		return err
	}

	s.events.PublishAssetChanged(MetaChangeEvent{
		EventType: EventDeleted,
		EntityID:  req.ID,
		Source:    req.Source,
		Before:    before,
	})
	return nil
}
```

> Note: behavior is identical to the legacy handlers (`meta_handler.go:159-372`).
> Store errors (e.g. `"asset not found: <id>"` from `DeleteAsset`) are returned
> unwrapped, so their `Error()` string is unchanged — that is why the regression
> suite in Task 4 still passes.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -run TestService_ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/meta_service.go internal/core/meta_service_test.go
git commit -m "feat(core): add MetadataService asset mutations"
```

---

### Task 3: Relation mutations + constraint helpers

**Files:**
- Modify: `internal/core/meta_service.go` (add methods)
- Modify: `internal/core/meta_service_test.go` (add tests)

**Interfaces:**
- Consumes: `Store.CreateRelation(*AssetRelation) error`, `Store.DeleteRelation(string) error`, `Store.GetRelation(string) (*AssetRelation, error)`, `Store.GetAsset`; `IsValidRelationType(RelationType) bool`; `constraintsViolationError([]ConstraintViolation) string` (`constraints.go:230`); `ConstraintsEvaluator.Check(*Asset, *Store) ([]ConstraintViolation, error)`; `EventPublisher.PublishRelationChanged`, `EventPublisher.PublishConstraintViolation`; request structs `CreateRelationRequest`, `DeleteRelationRequest`; enforcement consts `ConstraintsEnforcementWarn/Enforce/Disabled`.
- Produces:
  - `func (s *MetadataService) CreateRelation(req CreateRelationRequest) (*AssetRelation, error)`
  - `func (s *MetadataService) DeleteRelation(req DeleteRelationRequest) error`
  - `func (s *MetadataService) checkRelationConstraints(*AssetRelation) ([]ConstraintViolation, error)` (moved from `MetaHandler`)
  - `func (s *MetadataService) allowConstraintViolations([]ConstraintViolation) bool` (moved from `MetaHandler`)

- [ ] **Step 1: Write the failing test**

```go
// internal/core/meta_service_test.go  (append)

func TestService_CreateRelation_Validation(t *testing.T) {
	s := newTestService(t)

	_, err := s.CreateRelation(CreateRelationRequest{TargetAssetID: "b", RelationType: RelationPartOf})
	require.Equal(t, "source_asset_id is required", err.Error())

	_, err = s.CreateRelation(CreateRelationRequest{SourceAssetID: "a", RelationType: RelationPartOf})
	require.Equal(t, "target_asset_id is required", err.Error())

	_, err = s.CreateRelation(CreateRelationRequest{SourceAssetID: "a", TargetAssetID: "b"})
	require.Equal(t, "relation_type is required", err.Error())

	_, err = s.CreateRelation(CreateRelationRequest{SourceAssetID: "a", TargetAssetID: "b", RelationType: "bogus"})
	require.Equal(t, "invalid relation_type", err.Error())
	require.Equal(t, ErrValidation, KindOf(err))
}

func TestService_CreateRelation_Success(t *testing.T) {
	s := newTestService(t)
	a, err := s.CreateAsset(CreateAssetRequest{Name: "child"})
	require.NoError(t, err)
	b, err := s.CreateAsset(CreateAssetRequest{Name: "parent"})
	require.NoError(t, err)

	rel, err := s.CreateRelation(CreateRelationRequest{
		SourceAssetID: a.ID, TargetAssetID: b.ID, RelationType: RelationPartOf,
	})
	require.NoError(t, err)
	require.NotEmpty(t, rel.ID)
	require.Equal(t, RelationPartOf, rel.RelationType)
}

func TestService_DeleteRelation_Success(t *testing.T) {
	s := newTestService(t)
	a, _ := s.CreateAsset(CreateAssetRequest{Name: "c"})
	b, _ := s.CreateAsset(CreateAssetRequest{Name: "p"})
	rel, err := s.CreateRelation(CreateRelationRequest{
		SourceAssetID: a.ID, TargetAssetID: b.ID, RelationType: RelationPartOf,
	})
	require.NoError(t, err)

	require.NoError(t, s.DeleteRelation(DeleteRelationRequest{ID: rel.ID}))

	got, err := s.store.GetRelation(rel.ID)
	require.NoError(t, err)
	require.Nil(t, got)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run 'TestService_CreateRelation|TestService_DeleteRelation' -v`
Expected: FAIL — `MetadataService` has no `CreateRelation`/`DeleteRelation`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/core/meta_service.go  (append)

func (s *MetadataService) CreateRelation(req CreateRelationRequest) (*AssetRelation, error) {
	if req.SourceAssetID == "" {
		return nil, newServiceError(ErrValidation, "source_asset_id is required")
	}
	if req.TargetAssetID == "" {
		return nil, newServiceError(ErrValidation, "target_asset_id is required")
	}
	if req.RelationType == "" {
		return nil, newServiceError(ErrValidation, "relation_type is required")
	}
	if !IsValidRelationType(req.RelationType) {
		return nil, newServiceError(ErrValidation, "invalid relation_type")
	}

	relation := &AssetRelation{
		ID:            uuid.New().String(),
		SourceAssetID: req.SourceAssetID,
		TargetAssetID: req.TargetAssetID,
		RelationType:  req.RelationType,
		CreatedAt:     time.Now(),
		Metadata:      req.Metadata,
	}
	if err := s.store.CreateRelation(relation); err != nil {
		return nil, err
	}

	if violations, err := s.checkRelationConstraints(relation); err != nil {
		_ = s.store.DeleteRelation(relation.ID)
		return nil, err
	} else if !s.allowConstraintViolations(violations) {
		_ = s.store.DeleteRelation(relation.ID)
		return nil, newServiceError(ErrConstraint, constraintsViolationError(violations))
	}

	s.events.PublishRelationChanged(MetaChangeEvent{
		EventType: EventCreated,
		EntityID:  relation.ID,
		Source:    req.Source,
		After:     relation,
	})
	return relation, nil
}

func (s *MetadataService) DeleteRelation(req DeleteRelationRequest) error {
	if req.ID == "" {
		return newServiceError(ErrValidation, "id is required")
	}

	before, err := s.store.GetRelation(req.ID)
	if err != nil {
		return err
	}
	if err := s.store.DeleteRelation(req.ID); err != nil {
		return err
	}

	s.events.PublishRelationChanged(MetaChangeEvent{
		EventType: EventDeleted,
		EntityID:  req.ID,
		Source:    req.Source,
		Before:    before,
	})
	return nil
}

// checkRelationConstraints and allowConstraintViolations are moved verbatim from
// MetaHandler (meta_handler.go), with the receiver changed to *MetadataService.

func (s *MetadataService) checkRelationConstraints(relation *AssetRelation) ([]ConstraintViolation, error) {
	if s == nil || s.constraintEnforcement == ConstraintsEnforcementDisabled || s.constraints == nil || relation == nil {
		return nil, nil
	}

	assetIDs := []string{relation.SourceAssetID, relation.TargetAssetID}
	seen := map[string]bool{}
	var violations []ConstraintViolation
	for _, assetID := range assetIDs {
		if seen[assetID] {
			continue
		}
		seen[assetID] = true
		asset, err := s.store.GetAsset(assetID)
		if err != nil {
			return nil, err
		}
		if asset == nil {
			continue
		}
		assetViolations, err := s.constraints.Check(asset, s.store)
		if err != nil {
			return nil, err
		}
		violations = append(violations, assetViolations...)
	}
	return violations, nil
}

func (s *MetadataService) allowConstraintViolations(violations []ConstraintViolation) bool {
	if len(violations) == 0 || s.constraintEnforcement == ConstraintsEnforcementDisabled {
		return true
	}
	for _, violation := range violations {
		violation.EnforcementMode = s.constraintEnforcement
		s.events.PublishConstraintViolation(violation)
		log.Printf("[Meta] Constraint violation: %s", violation.Message)
	}
	return s.constraintEnforcement != ConstraintsEnforcementEnforce
}
```

> Add `"log"` to the imports of `meta_service.go` (used by `allowConstraintViolations`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -run 'TestService_CreateRelation|TestService_DeleteRelation' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/meta_service.go internal/core/meta_service_test.go
git commit -m "feat(core): add MetadataService relation mutations + constraint helpers"
```

---

### Task 4: Rewire `MetaHandler` to delegate to the service

**Files:**
- Modify: `internal/core/meta_handler.go` (struct, constructor, 5 mutation handlers; remove the 2 moved helpers)
- Test (regression, unchanged): `internal/core/meta_handler_test.go`

**Interfaces:**
- Consumes: `NewMetadataService` and all `MetadataService` methods (Tasks 2–3).
- Produces: `MetaHandler` with a `service *MetadataService` field; its public surface (`NewMetaHandler`, `NewMetaHandlerWithOptions`, `RegisterHandlers`) is unchanged.

- [ ] **Step 1: Run the existing regression suite (baseline, must be green before changes)**

Run: `go test ./internal/core/ -run 'TestMeta|TestAsset|TestRelation' -v`
Expected: PASS (these exercise the NATS contract we must preserve).

- [ ] **Step 2: Change the `MetaHandler` struct and constructor**

Replace the struct (`meta_handler.go:30-36`) and `NewMetaHandlerWithOptions` (`meta_handler.go:55-67`):

```go
// MetaHandler handles metadata NATS messages by delegating mutations to the
// shared MetadataService. Read/traversal handlers still use store/loader directly.
type MetaHandler struct {
	store       *Store
	loader      *TemplateLoader
	constraints *ConstraintsEvaluator
	service     *MetadataService
}
```

```go
func NewMetaHandlerWithOptions(store *Store, loader *TemplateLoader, opts MetaHandlerOptions) *MetaHandler {
	enforcement := opts.ConstraintEnforcement
	if enforcement == "" {
		enforcement = ConstraintsEnforcementWarn
	}
	return &MetaHandler{
		store:       store,
		loader:      loader,
		constraints: NewConstraintsEvaluator(loader),
		service:     NewMetadataService(store, loader, opts.Events, enforcement),
	}
}
```

(`NewMetaHandler` at `meta_handler.go:44-53` is unchanged — it still calls `NewMetaHandlerWithOptions`.)

- [ ] **Step 3: Replace the five mutation handler bodies with delegation**

`handleAssetCreate` (`meta_handler.go:159-209`):

```go
func (h *MetaHandler) handleAssetCreate(msg *nats.Msg) {
	var req CreateAssetRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}
	asset, err := h.service.CreateAsset(req)
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}
	log.Printf("[Meta] Asset created: %s (%s)", asset.Name, asset.ID)
	h.reply(msg, Response{Success: true, Data: asset})
}
```

`handleAssetUpdate` (`meta_handler.go:270-332`):

```go
func (h *MetaHandler) handleAssetUpdate(msg *nats.Msg) {
	var req UpdateAssetRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}
	updated, err := h.service.UpdateAsset(req)
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}
	log.Printf("[Meta] Asset updated: %s (%s)", updated.Name, updated.ID)
	h.reply(msg, Response{Success: true, Data: updated})
}
```

`handleAssetDelete` (`meta_handler.go:340-372`):

```go
func (h *MetaHandler) handleAssetDelete(msg *nats.Msg) {
	var req DeleteAssetRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}
	if err := h.service.DeleteAsset(req); err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}
	log.Printf("[Meta] Asset deleted: %s", req.ID)
	h.reply(msg, Response{Success: true})
}
```

`handleRelationCreate` (`meta_handler.go:523-585`):

```go
func (h *MetaHandler) handleRelationCreate(msg *nats.Msg) {
	var req CreateRelationRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}
	relation, err := h.service.CreateRelation(req)
	if err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}
	log.Printf("[Meta] Relation created: %s (%s -> %s, type: %s)",
		relation.ID, relation.SourceAssetID, relation.TargetAssetID, relation.RelationType)
	h.reply(msg, Response{Success: true, Data: relation})
}
```

`handleRelationDelete` (`meta_handler.go:693-725`):

```go
func (h *MetaHandler) handleRelationDelete(msg *nats.Msg) {
	var req DeleteRelationRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		h.reply(msg, Response{Success: false, Error: "invalid request format"})
		return
	}
	if err := h.service.DeleteRelation(req); err != nil {
		h.reply(msg, Response{Success: false, Error: err.Error()})
		return
	}
	log.Printf("[Meta] Relation deleted: %s", req.ID)
	h.reply(msg, Response{Success: true})
}
```

- [ ] **Step 4: Delete the now-duplicated helpers from `meta_handler.go`**

Remove `func (h *MetaHandler) checkRelationConstraints(...)` (`meta_handler.go:471-498`) and `func (h *MetaHandler) allowConstraintViolations(...)` (`meta_handler.go:500-510`) — they now live on `MetadataService` (Task 3). Leave `handleConstraintsCheck` (it uses `h.constraints.CheckAll(h.store)`) and all read/traversal handlers untouched.

- [ ] **Step 5: Build and run the full core suite (regression gate)**

Run: `go build ./... && go test ./internal/core/ -v`
Expected: PASS — every existing `meta_handler_test.go` assertion still holds (identical `Response` envelopes and error strings), and the new service tests pass. No test edits were needed.

- [ ] **Step 6: Commit**

```bash
git add internal/core/meta_handler.go
git commit -m "refactor(core): delegate MetaHandler mutations to MetadataService"
```

---

## Self-Review

**Spec coverage (Phase 1 of the design spec §5.2):**
- "Extract transport-agnostic logic into plain methods" → Tasks 2–3 (`CreateAsset/UpdateAsset/DeleteAsset/CreateRelation/DeleteRelation`).
- "Preserve exact existing behavior + NATS contract" → Task 4 Step 5 runs `meta_handler_test.go` unchanged as the gate; error strings preserved by returning `ServiceError.Msg` / store errors verbatim.
- "Typed/sentinel errors mapping to 404/409/400/422" → Task 1 (`ErrorKind` + `KindOf`). HTTP status mapping itself is **Phase 3** (out of this plan), but the classifier it needs is delivered here.
- "NATS handlers become thin wrappers" → Task 4.

**Placeholder scan:** No TBD/TODO; every code step shows complete code; every run step has an exact command + expected result.

**Type consistency:** `MetadataService`, `NewMetadataService(store, loader, events, enforcement)`, `CreateAsset(CreateAssetRequest) (*Asset, error)`, `DeleteAsset(DeleteAssetRequest) error`, `ServiceError{Kind, Msg}`, `KindOf`, `ErrValidation/ErrNotFound/ErrConflict/ErrConstraint` are used identically across Tasks 1–4. `checkRelationConstraints`/`allowConstraintViolations` keep their exact signatures when moved (receiver `*MetadataService`).

## Out of scope (later phases / plans)

- Phase 2: remove auto-registration + `unknown_asset_policy`.
- Phase 3: HTTP write endpoints + `KindOf`→HTTP status mapping + auth hardening.
- Phase 4: templates → DB (migration 0004, import/export).
- Phase 5: embedded UI + live refresh.
