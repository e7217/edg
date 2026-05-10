package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func newConnectedClient(t *testing.T, url string) *Client {
	t.Helper()
	c := NewClient(Options{URL: url, RequestTimeout: 2 * time.Second})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClientConnectAndClose(t *testing.T) {
	url := startTestNATSServer(t)
	c := NewClient(Options{URL: url})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if !c.IsConnected() {
		t.Errorf("expected connected")
	}
	if err := c.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	if c.IsConnected() {
		t.Errorf("expected disconnected after close")
	}
	// Connect again should succeed.
	if err := c.Connect(context.Background()); err != nil {
		t.Errorf("reconnect: %v", err)
	}
	_ = c.Close()
}

func TestClientConnectInvalidURL(t *testing.T) {
	c := NewClient(Options{URL: "nats://127.0.0.1:1", ConnectTimeout: 200 * time.Millisecond})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrConnection) {
		t.Errorf("error chain missing ErrConnection: %v", err)
	}
}

func TestPublishAssetData(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)

	got := make(chan AssetData, 1)
	sub, err := control.Subscribe(SubjectAssetData, func(msg *nats.Msg) {
		var ad AssetData
		if err := json.Unmarshal(msg.Data, &ad); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		got <- ad
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	c := newConnectedClient(t, url)
	n := 25.5
	if err := c.PublishAssetData(context.Background(), AssetData{
		AssetID: "sensor-001",
		Values:  []TagValue{{Name: "temp", Quality: QualityGood, Number: &n, Unit: "°C"}},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case ad := <-got:
		if ad.AssetID != "sensor-001" {
			t.Errorf("AssetID: %q", ad.AssetID)
		}
		if ad.Timestamp == 0 {
			t.Errorf("Timestamp should be auto-set")
		}
		if len(ad.Values) != 1 || ad.Values[0].Number == nil || *ad.Values[0].Number != 25.5 {
			t.Errorf("Values mismatch: %+v", ad.Values)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive published data")
	}
}

func TestPublishAssetDataNotConnected(t *testing.T) {
	c := NewClient(Options{URL: "nats://127.0.0.1:1"})
	err := c.PublishAssetData(context.Background(), AssetData{AssetID: "x"})
	if err == nil || !errors.Is(err, ErrPublish) {
		t.Errorf("expected ErrPublish chain, got %v", err)
	}
}

// reply registers a request/reply handler that echoes a coreResponse with the
// given outcome. captured stores the last decoded request payload for the
// caller to inspect.
type captured struct {
	mu  sync.Mutex
	raw []byte
}

func (c *captured) set(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.raw = append([]byte(nil), b...)
}

func (c *captured) get() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.raw
}

func reply(t *testing.T, nc *nats.Conn, subject string, cap *captured, success bool, data any, errMsg string) {
	t.Helper()
	_, err := nc.Subscribe(subject, func(msg *nats.Msg) {
		if cap != nil {
			cap.set(msg.Data)
		}
		var dataBytes []byte
		if data != nil {
			b, err := json.Marshal(data)
			if err != nil {
				t.Errorf("marshal reply data: %v", err)
				return
			}
			dataBytes = b
		}
		resp := coreResponse{Success: success, Data: dataBytes, Error: errMsg}
		out, err := json.Marshal(resp)
		if err != nil {
			t.Errorf("marshal coreResponse: %v", err)
			return
		}
		_ = msg.Respond(out)
	})
	if err != nil {
		t.Fatalf("subscribe %s: %v", subject, err)
	}
}

func TestCreateAssetSuccess(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)

	asset := Asset{
		ID:        "abc",
		Name:      "sensor-9",
		Source:    SourceManual,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	cap := &captured{}
	reply(t, control, SubjectAssetCreate, cap, true, asset, "")

	c := newConnectedClient(t, url)
	got, err := c.CreateAsset(context.Background(), CreateAssetRequest{Name: "sensor-9"})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}
	if got == nil || got.Name != "sensor-9" {
		t.Errorf("got: %+v", got)
	}
	// Verify the request payload that core received.
	var req CreateAssetRequest
	if err := json.Unmarshal(cap.get(), &req); err != nil {
		t.Fatalf("decode captured request: %v", err)
	}
	if req.Name != "sensor-9" {
		t.Errorf("request Name: %q", req.Name)
	}
}

func TestCreateAssetFailureBecomesCoreError(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	reply(t, control, SubjectAssetCreate, nil, false, nil, "name is required")

	c := newConnectedClient(t, url)
	_, err := c.CreateAsset(context.Background(), CreateAssetRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrCore) {
		t.Errorf("error chain missing ErrCore: %v", err)
	}
	var ce *CoreError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CoreError, got %T", err)
	}
	if ce.Message != "name is required" {
		t.Errorf("CoreError.Message: %q", ce.Message)
	}
}

func TestGetAssetNotFoundReturnsNilNil(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	reply(t, control, SubjectAssetGet, nil, false, nil, "asset not found")

	c := newConnectedClient(t, url)
	got, err := c.GetAsset(context.Background(), GetAssetRequest{ID: "nope"})
	if err != nil {
		t.Errorf("expected nil error for not found, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil asset, got %+v", got)
	}
}

func TestGetAssetSuccess(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	asset := Asset{ID: "abc", Name: "sensor-1", Source: SourceManual}
	reply(t, control, SubjectAssetGet, nil, true, asset, "")

	c := newConnectedClient(t, url)
	got, err := c.GetAsset(context.Background(), GetAssetRequest{ID: "abc"})
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if got == nil || got.ID != "abc" {
		t.Errorf("got: %+v", got)
	}
}

func TestListAssets(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	assets := []Asset{
		{ID: "a", Name: "sensor-a", Source: SourceManual},
		{ID: "b", Name: "sensor-b", Source: SourceAuto},
	}
	reply(t, control, SubjectAssetList, nil, true, assets, "")

	c := newConnectedClient(t, url)
	got, err := c.ListAssets(context.Background())
	if err != nil {
		t.Fatalf("ListAssets: %v", err)
	}
	if len(got) != 2 || got[1].Source != SourceAuto {
		t.Errorf("ListAssets: %+v", got)
	}
}

func TestUpdateAndDeleteAsset(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)

	updated := Asset{ID: "abc", Name: "renamed", Source: SourceManual}
	reply(t, control, SubjectAssetUpdate, nil, true, updated, "")
	reply(t, control, SubjectAssetDelete, nil, true, nil, "")

	c := newConnectedClient(t, url)
	got, err := c.UpdateAsset(context.Background(), UpdateAssetRequest{ID: "abc", Name: "renamed"})
	if err != nil {
		t.Fatalf("UpdateAsset: %v", err)
	}
	if got == nil || got.Name != "renamed" {
		t.Errorf("UpdateAsset: %+v", got)
	}
	if err := c.DeleteAsset(context.Background(), DeleteAssetRequest{ID: "abc"}); err != nil {
		t.Errorf("DeleteAsset: %v", err)
	}
}

func TestRelationCRUD(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)

	rel := AssetRelation{
		ID:            "r-1",
		SourceAssetID: "a",
		TargetAssetID: "b",
		RelationType:  RelationPartOf,
		CreatedAt:     time.Now().UTC(),
	}
	reply(t, control, SubjectRelationCreate, nil, true, rel, "")
	reply(t, control, SubjectRelationGet, nil, true, rel, "")
	reply(t, control, SubjectRelationList, nil, true, []AssetRelation{rel}, "")
	reply(t, control, SubjectRelationDelete, nil, true, nil, "")

	c := newConnectedClient(t, url)
	created, err := c.CreateRelation(context.Background(), CreateRelationRequest{
		SourceAssetID: "a", TargetAssetID: "b", RelationType: RelationPartOf,
	})
	if err != nil || created == nil || created.ID != "r-1" {
		t.Fatalf("CreateRelation: %v %+v", err, created)
	}
	got, err := c.GetRelation(context.Background(), "r-1")
	if err != nil || got == nil {
		t.Fatalf("GetRelation: %v %+v", err, got)
	}
	listed, err := c.ListRelations(context.Background(), ListRelationsRequest{AssetID: "a"})
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListRelations: %v %+v", err, listed)
	}
	if err := c.DeleteRelation(context.Background(), "r-1"); err != nil {
		t.Errorf("DeleteRelation: %v", err)
	}
}

func TestGetRelationNotFound(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)
	reply(t, control, SubjectRelationGet, nil, false, nil, "relation not found")

	c := newConnectedClient(t, url)
	got, err := c.GetRelation(context.Background(), "nope")
	if err != nil {
		t.Errorf("expected nil error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil relation, got %+v", got)
	}
}

func TestSubscribeMetaChanges(t *testing.T) {
	url := startTestNATSServer(t)
	control := connectControl(t, url)

	c := newConnectedClient(t, url)

	received := make(chan MetaChangeEvent, 2)
	sub, err := c.SubscribeMetaChanges(func(ev MetaChangeEvent) {
		received <- ev
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	// Allow the subscription to register before publishing.
	if err := control.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	publishEvent := func(subject string, ev MetaChangeEvent) {
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := control.Publish(subject, b); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	publishEvent(SubjectAssetChanged, MetaChangeEvent{
		SchemaVersion: 1,
		EventType:     EventCreated,
		EntityType:    EntityAsset,
		EntityID:      "sensor-1",
		Source:        SourceManual,
		Timestamp:     time.Now().UTC(),
	})
	publishEvent(SubjectRelationChanged, MetaChangeEvent{
		SchemaVersion: 1,
		EventType:     EventDeleted,
		EntityType:    EntityRelation,
		EntityID:      "rel-1",
		Source:        SourceManual,
		Timestamp:     time.Now().UTC(),
	})

	deadline := time.After(2 * time.Second)
	got := make(map[string]bool)
	for len(got) < 2 {
		select {
		case ev := <-received:
			got[ev.EntityType] = true
		case <-deadline:
			t.Fatalf("timeout: received %v", got)
		}
	}
	if !got[EntityAsset] || !got[EntityRelation] {
		t.Errorf("missing events: %v", got)
	}
}

func TestRequestRespectsContextDeadline(t *testing.T) {
	url := startTestNATSServer(t)
	// No subscriber for SubjectAssetGet — the request will time out.
	c := newConnectedClient(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := c.GetAsset(ctx, GetAssetRequest{ID: "x"})
	if err == nil {
		t.Fatal("expected error on timeout")
	}
}

func TestWrap(t *testing.T) {
	url := startTestNATSServer(t)
	external, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer external.Close()

	c := Wrap(external, Options{})
	if !c.IsConnected() {
		t.Errorf("Wrap should already be connected")
	}
	// Close on a Wrap'd client should be a no-op for the external conn.
	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if !external.IsConnected() {
		t.Errorf("external connection should not be closed by Wrap.Close")
	}
}
