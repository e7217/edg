package core

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChaosJetStream_BacklogRecoversWhenConsumerAttaches(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "BACKLOG_TEST",
		Subjects: []string{"platform.data.>"},
		Storage:  nats.MemoryStorage,
		MaxMsgs:  100,
	})
	require.NoError(t, err)

	handler := NewDataHandler(js, nil)
	for i := 0; i < 10; i++ {
		handler.HandleAssetData(assetDataMessage(t, fmt.Sprintf("sensor-%03d", i), float64(i)))
	}

	info, err := js.StreamInfo("BACKLOG_TEST")
	require.NoError(t, err)
	require.Equal(t, uint64(10), info.State.Msgs)

	sub, err := js.PullSubscribe("platform.data.validated", "vm-writer")
	require.NoError(t, err)
	defer sub.Unsubscribe()

	msgs, err := sub.Fetch(10, nats.MaxWait(2*time.Second))
	require.NoError(t, err)
	require.Len(t, msgs, 10)
	for _, msg := range msgs {
		require.NoError(t, msg.Ack())
	}
}

func TestChaosJetStream_DiscardOldUnderMaxBytesPressure(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "PRESSURE_TEST",
		Subjects: []string{"pressure.data"},
		Storage:  nats.FileStorage,
		MaxBytes: 96,
		Discard:  nats.DiscardOld,
	})
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		_, err := js.Publish("pressure.data", []byte(fmt.Sprintf("message-%d-with-padding", i)))
		require.NoError(t, err)
	}

	info, err := js.StreamInfo("PRESSURE_TEST")
	require.NoError(t, err)
	assert.Less(t, info.State.Msgs, uint64(5))
	assert.Greater(t, info.State.FirstSeq, uint64(1))
}

func TestChaosDataHandler_ConcurrentUndeclaredPassThrough(t *testing.T) {
	_, _, js := startTestNATSServer(t, true)

	_, err := js.AddStream(&nats.StreamConfig{
		Name:     "RACE_TEST",
		Subjects: []string{"platform.data.>"},
		Storage:  nats.MemoryStorage,
	})
	require.NoError(t, err)

	store, err := NewStore(filepath.Join(t.TempDir(), "metadata.db"))
	require.NoError(t, err)
	defer store.Close()

	handler := NewDataHandler(js, store)
	msg := assetDataMessage(t, "shared-asset", 42)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handler.HandleAssetData(msg)
		}()
	}
	wg.Wait()

	// pass_through (default): undeclared assets are never auto-registered,
	// even under concurrency, and all messages still pass through.
	asset, err := store.GetAsset("shared-asset")
	require.NoError(t, err)
	require.Nil(t, asset)

	assets, err := store.ListAssets()
	require.NoError(t, err)
	assert.Len(t, assets, 0)
	assert.Equal(t, 100, handler.GetDataCount())
}

func assetDataMessage(t *testing.T, assetID string, value float64) *nats.Msg {
	t.Helper()

	data := &AssetData{
		AssetID:   assetID,
		Timestamp: time.Now().UnixNano(),
		Values: []TagValue{
			{Name: "temperature", Number: &value, Unit: "celsius", Quality: "good"},
		},
	}
	payload, err := json.Marshal(data)
	require.NoError(t, err)

	return &nats.Msg{
		Subject: "platform.data.asset",
		Data:    payload,
	}
}
