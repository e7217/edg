package core

import (
	"encoding/json"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTestNATSServer starts an embedded NATS server for testing
func startTestNATSServer(t *testing.T, enableJetStream bool) (*natsserver.Server, *nats.Conn, nats.JetStreamContext) {
	opts := &natsserver.Options{
		Port:      -1, // random port
		JetStream: enableJetStream,
	}

	if enableJetStream {
		opts.StoreDir = t.TempDir() // use temporary directory
	}

	ns, err := natsserver.NewServer(opts)
	require.NoError(t, err)

	go ns.Start()

	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server not ready")
	}

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)

	var js nats.JetStreamContext
	if enableJetStream {
		js, err = nc.JetStream()
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		nc.Close()
		ns.Shutdown()
	})

	return ns, nc, js
}

// TestHandleAssetData_WithJetStream tests data processing with JetStream publishing
func TestHandleAssetData_WithJetStream(t *testing.T) {
	_, nc, js := startTestNATSServer(t, true)

	// Create JetStream stream
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST_STREAM",
		Subjects: []string{"platform.data.>"},
		Storage:  nats.MemoryStorage,
	})
	require.NoError(t, err)

	handler := NewDataHandler(js, nil)

	tempValue := 25.5
	data := &AssetData{
		AssetID:   "sensor-001",
		Timestamp: 1234567890,
		Values: []TagValue{
			{Name: "temperature", Number: &tempValue, Unit: "celsius", Quality: "good"},
		},
	}

	jsonData, err := json.Marshal(data)
	require.NoError(t, err)

	// Subscribe to validated data
	received := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("platform.data.validated", func(msg *nats.Msg) {
		received <- msg
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Process message
	msg := &nats.Msg{
		Subject: "platform.data.asset",
		Data:    jsonData,
	}
	handler.HandleAssetData(msg)

	// Wait for message to be published
	select {
	case receivedMsg := <-received:
		assert.Equal(t, jsonData, receivedMsg.Data)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for published message")
	}

	// Verify data was stored
	assert.Equal(t, 1, handler.GetDataCount())
}

// TestHandleAssetData_WithJetStreamAndStore tests both JetStream and auto-registration
func TestHandleAssetData_WithJetStreamAndStore(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)

	// Create JetStream stream
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "TEST_STREAM",
		Subjects: []string{"platform.data.>"},
		Storage:  nats.MemoryStorage,
	})
	require.NoError(t, err)

	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	handler := NewDataHandler(js, store)

	tempValue := 25.5
	data := &AssetData{
		AssetID:   "new-sensor",
		Timestamp: 1234567890,
		Values: []TagValue{
			{Name: "temperature", Number: &tempValue, Unit: "celsius"},
		},
	}

	jsonData, err := json.Marshal(data)
	require.NoError(t, err)

	msg := &nats.Msg{
		Subject: "platform.data.asset",
		Data:    jsonData,
	}

	// Process message
	handler.HandleAssetData(msg)

	// Verify asset was auto-registered
	asset, err := store.GetAsset("new-sensor")
	require.NoError(t, err)
	require.NotNil(t, asset)
	assert.Equal(t, "new-sensor", asset.ID)

	// Verify data was stored
	assert.Equal(t, 1, handler.GetDataCount())
}

// TestJetStreamPublish_MessagePersistence tests message persistence in JetStream
func TestJetStreamPublish_MessagePersistence(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)

	// Create JetStream stream
	streamInfo, err := js.AddStream(&nats.StreamConfig{
		Name:     "PERSIST_TEST",
		Subjects: []string{"platform.data.>"},
		Storage:  nats.MemoryStorage,
		MaxMsgs:  100,
	})
	require.NoError(t, err)
	require.NotNil(t, streamInfo)

	handler := NewDataHandler(js, nil)

	// Publish multiple messages
	for i := 0; i < 5; i++ {
		tempValue := float64(i)
		data := &AssetData{
			AssetID:   "sensor-001",
			Timestamp: int64(i),
			Values: []TagValue{
				{Name: "temp", Number: &tempValue},
			},
		}

		jsonData, err := json.Marshal(data)
		require.NoError(t, err)

		msg := &nats.Msg{
			Subject: "platform.data.asset",
			Data:    jsonData,
		}
		handler.HandleAssetData(msg)
	}

	// Wait a bit for async publishing
	time.Sleep(100 * time.Millisecond)

	// Verify messages are persisted in stream
	streamInfo, err = js.StreamInfo("PERSIST_TEST")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, streamInfo.State.Msgs, uint64(5))

	// Verify we can retrieve messages
	sub, err := js.SubscribeSync("platform.data.validated")
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Try to receive messages
	for i := 0; i < 5; i++ {
		msg, err := sub.NextMsg(1 * time.Second)
		if err != nil {
			t.Logf("Warning: Could not receive message %d: %v", i, err)
			continue
		}
		assert.NotNil(t, msg)
		assert.Greater(t, len(msg.Data), 0)
	}
}

// TestNewDataHandler_WithNilJetStream tests handler creation with nil JetStream
func TestNewDataHandler_WithNilJetStream(t *testing.T) {
	handler := NewDataHandler(nil, nil)
	require.NotNil(t, handler)
	assert.NotNil(t, handler.data)
	assert.Equal(t, 0, len(handler.data))
}

// TestHandleAssetData_JetStreamPublishError tests handling of publish errors
func TestHandleAssetData_JetStreamPublishError(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)

	// Create stream with limited subjects
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "LIMITED_STREAM",
		Subjects: []string{"allowed.>"},
		Storage:  nats.MemoryStorage,
	})
	require.NoError(t, err)

	handler := NewDataHandler(js, nil)

	data := &AssetData{
		AssetID:   "sensor-001",
		Timestamp: 1234567890,
		Values:    []TagValue{{Name: "temp", Number: new(float64)}},
	}

	jsonData, err := json.Marshal(data)
	require.NoError(t, err)

	msg := &nats.Msg{
		Subject: "platform.data.asset",
		Data:    jsonData,
	}

	// This should log an error but not panic
	// The message won't be published to JetStream because the subject doesn't match
	handler.HandleAssetData(msg)

	// Data should still be stored in memory
	assert.Equal(t, 1, handler.GetDataCount())
}
