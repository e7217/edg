package core

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// The data contract (ADR 0010) is what platform.data.validated promises about
// every message on it. Before it existed "validated" meant "decoded as JSON":
// an empty object, a string where the template declares a number, and adapter
// metadata contradicting master data all went through to storage.
//
// The contract has two layers:
//
//   - The envelope rules hold for every message, declared asset or not: an
//     asset_id, at least one reading, one reading per value, a millisecond
//     timestamp.
//   - The declaration rules hold only where master data declares something:
//     a point list or template for the asset fixes each tag's value type and
//     unit. Master data that declares nothing constrains nothing.
//
// A violation removes the smallest thing that is wrong. A malformed envelope
// rejects the message; a malformed value drops that value and keeps its
// siblings, because one bad register must not take the other forty-nine of a
// Modbus poll with it; a reserved metadata key drops that key. Nothing is
// removed silently: every removal is counted by reason and dead-lettered with
// the original payload.

// Contract modes.
const (
	// DataContractEnforce removes violating messages, values and keys.
	DataContractEnforce = "enforce"
	// DataContractWarn counts and logs violations but lets the data through
	// unchanged. It exists for the upgrade: a plant can measure what enforce
	// would remove before turning it on.
	DataContractWarn = "warn"
)

// Violation reasons. They are the label values of
// edg_core_data_contract_violations_total and appear in dead-letter records.
const (
	// Message-level: the message is rejected.
	ViolationMissingAssetID = "missing_asset_id"
	ViolationNoValues       = "no_values"
	ViolationTimestampUnit  = "timestamp_not_milliseconds"

	// Value-level: the value is dropped.
	ViolationEmptyName        = "empty_name"
	ViolationDuplicateName    = "duplicate_name"
	ViolationNoReading        = "no_reading"
	ViolationMultipleReadings = "multiple_readings"
	ViolationNonFinite        = "non_finite_number"
	ViolationTypeMismatch     = "type_mismatch"
	ViolationUnitMismatch     = "unit_mismatch"

	// Metadata-level: the key is dropped.
	ViolationReservedMetadata = "reserved_metadata_key"
)

// ViolationReasons lists every reason, in the order the metric declares them.
var ViolationReasons = []string{
	ViolationMissingAssetID, ViolationNoValues, ViolationTimestampUnit,
	ViolationEmptyName, ViolationDuplicateName, ViolationNoReading,
	ViolationMultipleReadings, ViolationNonFinite, ViolationTypeMismatch,
	ViolationUnitMismatch, ViolationReservedMetadata,
}

// minMillisTimestamp separates epoch milliseconds from epoch seconds. Any
// seconds value from the last fifty years is below it and any milliseconds
// value after September 2001 is above it, so a timestamp under it is either a
// seconds value -- stored as a date in January 1970 -- or garbage.
const minMillisTimestamp = int64(1_000_000_000_000)

// ReservedMetadataKeys are the tag keys the sink writes itself. Metadata under
// one of these names would produce a line with the key twice, which
// VictoriaMetrics rejects -- failing the whole batch and redelivering it
// forever.
var ReservedMetadataKeys = map[string]bool{
	"asset_id": true,
	"name":     true,
	"unit":     true,
	"quality":  true,
	// value carries a text reading (see appendAssetDataLines).
	"value": true,
}

// Violation is one broken rule.
type Violation struct {
	Reason string `json:"reason"`
	// Tag is the value name or metadata key the violation is about; empty for
	// a message-level violation.
	Tag    string `json:"tag,omitempty"`
	Detail string `json:"detail"`
}

// ContractResult is the outcome of checking one message.
type ContractResult struct {
	Violations []Violation
	// Rejected means nothing in the message may be published.
	Rejected bool
	// DroppedValues counts values removed by enforcement.
	DroppedValues int
	// TimestampFilled means the message carried no timestamp and was stamped
	// with the time the core received it.
	TimestampFilled bool
}

// declaredTag is master data's claim about one tag of one asset.
type declaredTag struct {
	ValueType string
	Unit      string
}

// assetProfile is what master data declares about one asset, resolved once
// and cached, so the ingest path does not query SQLite per message.
type assetProfile struct {
	Exists bool
	Tags   map[string]declaredTag
}

// ContractChecker resolves declarations and applies the contract.
type ContractChecker struct {
	store *Store
	mode  string
	now   func() time.Time

	mu            sync.Mutex
	cache         map[string]*assetProfile
	cacheVersion  uint64
	subscriptions []*nats.Subscription
}

// NewContractChecker builds a checker. A nil store checks the envelope only.
func NewContractChecker(store *Store, mode string) *ContractChecker {
	if mode == "" {
		mode = DataContractEnforce
	}
	return &ContractChecker{
		store: store,
		mode:  mode,
		now:   time.Now,
		cache: make(map[string]*assetProfile),
	}
}

// Mode reports the configured mode.
func (c *ContractChecker) Mode() string { return c.mode }

// Profile returns the cached declarations for an asset.
func (c *ContractChecker) Profile(assetID string) (*assetProfile, error) {
	if c.store == nil {
		return &assetProfile{Exists: false}, nil
	}
	c.mu.Lock()
	if p, ok := c.cache[assetID]; ok {
		c.mu.Unlock()
		return p, nil
	}
	version := c.cacheVersion
	c.mu.Unlock()

	p, err := c.loadProfile(assetID)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	// A flush that raced the load means p may already be stale; returning it
	// for this message is fine, caching it is not. Undeclared ids are never
	// cached: the cache is then bounded by master data, not by whatever ids
	// adapters happen to send.
	if p.Exists && c.cacheVersion == version {
		c.cache[assetID] = p
	}
	c.mu.Unlock()
	return p, nil
}

func (c *ContractChecker) loadProfile(assetID string) (*assetProfile, error) {
	asset, err := c.store.GetAsset(assetID)
	if err != nil {
		return nil, err
	}
	if asset == nil {
		return &assetProfile{Exists: false}, nil
	}
	p := &assetProfile{Exists: true, Tags: map[string]declaredTag{}}

	// Template resources first, then point declarations over them: a point
	// list is about this asset specifically, a template about its kind.
	if asset.TemplateName != "" {
		tmpl, err := c.store.GetTemplate(asset.TemplateName)
		if err != nil {
			return nil, err
		}
		if tmpl != nil {
			for _, r := range tmpl.Resources {
				p.Tags[r.Name] = declaredTag{ValueType: r.ValueType, Unit: r.Unit}
			}
		}
	}
	pl, err := c.store.GetPointList(assetID)
	if err != nil {
		return nil, err
	}
	if pl != nil {
		for _, pt := range pl.Points {
			p.Tags[pt.Name] = declaredTag{ValueType: pt.ValueType, Unit: pt.Unit}
		}
	}
	return p, nil
}

// Flush drops every cached profile.
func (c *ContractChecker) Flush() {
	c.mu.Lock()
	c.cache = make(map[string]*assetProfile)
	c.cacheVersion++
	c.mu.Unlock()
}

// Start subscribes to the master-data change events that can alter a profile.
//
// Templates are not among them: they change only through -import-templates,
// which the running core does not observe either way (the TemplateLoader has
// the same limit), so a template import takes effect on restart.
func (c *ContractChecker) Start(nc *nats.Conn) error {
	if c == nil || nc == nil {
		return nil
	}
	for _, subject := range []string{SubjectAssetChanged, SubjectPointsChanged} {
		sub, err := nc.Subscribe(subject, func(*nats.Msg) { c.Flush() })
		if err != nil {
			return fmt.Errorf("failed to subscribe contract checker to %s: %w", subject, err)
		}
		c.mu.Lock()
		c.subscriptions = append(c.subscriptions, sub)
		c.mu.Unlock()
	}
	return nil
}

// Stop removes the subscriptions made by Start.
func (c *ContractChecker) Stop() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	subs := c.subscriptions
	c.subscriptions = nil
	c.mu.Unlock()
	for _, sub := range subs {
		if err := sub.Unsubscribe(); err != nil {
			return err
		}
	}
	return nil
}

// Apply checks data against the contract and, in enforce mode, removes what
// violates it. profile may be nil, which means "declares nothing".
func (c *ContractChecker) Apply(data *AssetData, profile *assetProfile) ContractResult {
	var res ContractResult
	enforce := c.mode != DataContractWarn

	if data.AssetID == "" {
		res.Violations = append(res.Violations, Violation{
			Reason: ViolationMissingAssetID, Detail: "asset_id is empty"})
	}
	if len(data.Values) == 0 {
		res.Violations = append(res.Violations, Violation{
			Reason: ViolationNoValues, Detail: "values is empty"})
	}
	switch {
	case data.Timestamp <= 0:
		// Stamping is not a violation: an adapter that leaves the time to the
		// gateway is using the contract, not breaking it. It is still counted,
		// because receive time includes the adapter-to-core delay.
		data.Timestamp = c.now().UnixMilli()
		res.TimestampFilled = true
	case data.Timestamp < minMillisTimestamp:
		res.Violations = append(res.Violations, Violation{
			Reason: ViolationTimestampUnit,
			Detail: fmt.Sprintf("timestamp %d is not epoch milliseconds (seconds would be stored as January 1970)", data.Timestamp),
		})
	}
	if len(res.Violations) > 0 {
		// The envelope is wrong; value checks would only add noise.
		res.Rejected = enforce
		return res
	}

	var tags map[string]declaredTag
	if profile != nil {
		tags = profile.Tags
	}

	kept := data.Values[:0:0]
	seen := make(map[string]bool, len(data.Values))
	for i := range data.Values {
		v := data.Values[i]
		if violation, ok := checkValue(&v, seen, tags); !ok {
			res.Violations = append(res.Violations, violation)
			if enforce {
				res.DroppedValues++
				continue
			}
		}
		kept = append(kept, v)
	}
	data.Values = kept

	for key := range data.Metadata {
		if ReservedMetadataKeys[key] {
			res.Violations = append(res.Violations, Violation{
				Reason: ViolationReservedMetadata, Tag: key,
				Detail: fmt.Sprintf("metadata key %q is written by the sink itself", key),
			})
			if enforce {
				delete(data.Metadata, key)
			}
		}
	}

	if enforce && len(data.Values) == 0 {
		res.Rejected = true
	}
	return res
}

// checkValue applies the value rules. It fills a missing unit from the
// declaration, which is why it takes a pointer.
func checkValue(v *TagValue, seen map[string]bool, tags map[string]declaredTag) (Violation, bool) {
	if v.Name == "" {
		return Violation{Reason: ViolationEmptyName, Detail: "value has no name"}, false
	}
	if seen[v.Name] {
		return Violation{Reason: ViolationDuplicateName, Tag: v.Name,
			Detail: "name appears more than once in the message; the first is kept"}, false
	}
	seen[v.Name] = true

	readings := 0
	kind := ""
	if v.Number != nil {
		readings++
		kind = ValueTypeNumber
	}
	if v.Text != nil {
		readings++
		kind = ValueTypeText
	}
	if v.Flag != nil {
		readings++
		kind = ValueTypeFlag
	}
	switch readings {
	case 0:
		return Violation{Reason: ViolationNoReading, Tag: v.Name,
			Detail: "none of number, text, flag is set"}, false
	case 1:
	default:
		return Violation{Reason: ViolationMultipleReadings, Tag: v.Name,
			Detail: "more than one of number, text, flag is set"}, false
	}
	if v.Number != nil && (math.IsNaN(*v.Number) || math.IsInf(*v.Number, 0)) {
		return Violation{Reason: ViolationNonFinite, Tag: v.Name,
			Detail: "number is NaN or infinite"}, false
	}

	decl, declared := tags[v.Name]
	if !declared {
		return Violation{}, true
	}
	if decl.ValueType != "" && decl.ValueType != kind {
		return Violation{Reason: ViolationTypeMismatch, Tag: v.Name,
			Detail: fmt.Sprintf("declared %s, received %s", decl.ValueType, kind)}, false
	}
	if decl.Unit != "" {
		switch v.Unit {
		case "":
			v.Unit = decl.Unit
		case decl.Unit:
		default:
			// Refused rather than overwritten: relabelling 77 °F as °C would
			// store a wrong number that looks right.
			return Violation{Reason: ViolationUnitMismatch, Tag: v.Name,
				Detail: fmt.Sprintf("declared unit %q, received %q", decl.Unit, v.Unit)}, false
		}
	}
	return Violation{}, true
}
