package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/tbrandon/mbserver"
)

func startTestServer(t *testing.T, hr map[uint16]uint16, ir map[uint16]uint16) (addr string, stop func()) {
	t.Helper()
	srv := mbserver.NewServer()
	for a, v := range hr {
		if int(a) >= len(srv.HoldingRegisters) {
			t.Fatalf("hr address %d out of range", a)
		}
		srv.HoldingRegisters[a] = v
	}
	for a, v := range ir {
		if int(a) >= len(srv.InputRegisters) {
			t.Fatalf("ir address %d out of range", a)
		}
		srv.InputRegisters[a] = v
	}

	// Bind to ephemeral port via net.Listen so we can grab the address.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr = l.Addr().String()
	l.Close()

	if err := srv.ListenTCP(addr); err != nil {
		t.Fatalf("ListenTCP: %v", err)
	}
	// Wait for the server to accept connections.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return addr, srv.Close
		}
		time.Sleep(20 * time.Millisecond)
	}
	srv.Close()
	t.Fatalf("test modbus server did not come up at %s", addr)
	return "", nil
}

func TestAdapterReadsAndDecodes(t *testing.T) {
	hr := map[uint16]uint16{
		10: 250,    // int16, scale 0.1 -> 25.0
		20: 0x4048, // float32 ABCD high word of 3.14
		21: 0xF5C3, // float32 ABCD low word of 3.14
	}
	ir := map[uint16]uint16{
		5: 0xFFFF, // int16 -> -1.0
	}

	addr, stop := startTestServer(t, hr, ir)
	defer stop()

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatal(err)
	}

	cfg := &ModbusConfig{
		Version:      1,
		Host:         host,
		Port:         port,
		UnitID:       1,
		PollInterval: 1.0,
		Timeout:      1.0,
		Registers: []RegisterSpec{
			{Name: "temperature", Function: "holding", Address: 10, Type: "int16", Scale: 0.1, Unit: "°C", WordOrder: "ABCD"},
			{Name: "pressure", Function: "holding", Address: 20, Type: "float32", WordOrder: "ABCD", Scale: 1.0, Unit: "bar"},
			{Name: "status", Function: "input", Address: 5, Type: "int16", Scale: 1.0, WordOrder: "ABCD"},
		},
	}

	dev := NewModbusDevice(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := dev.ConnectDevice(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer dev.DisconnectDevice(ctx)

	values, err := dev.Collect(ctx)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	byName := map[string]float64{}
	for _, v := range values {
		if v.Number != nil {
			byName[v.Name] = *v.Number
		}
	}

	if got, want := byName["temperature"], 25.0; abs(got-want) > 1e-6 {
		t.Errorf("temperature: got %v, want %v", got, want)
	}
	if got, want := byName["pressure"], 3.14; abs(got-want) > 1e-3 {
		t.Errorf("pressure: got %v, want ~%v", got, want)
	}
	if got, want := byName["status"], -1.0; got != want {
		t.Errorf("status: got %v, want %v", got, want)
	}
}

func TestPacketBytesToWords(t *testing.T) {
	// Sanity check: goburrow returns raw bytes, 2 per register, big-endian.
	raw := []byte{0x12, 0x34, 0x56, 0x78}
	words := bytesToWords(raw)
	if len(words) != 2 || words[0] != 0x1234 || words[1] != 0x5678 {
		t.Errorf("bytesToWords mismatch: %v", words)
	}
}

func TestPacketBytesToWordsOddLength(t *testing.T) {
	_, err := bytesToWordsErr([]byte{0x12, 0x34, 0x56})
	if err == nil {
		t.Fatal("expected error for odd-length packet")
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// bytesToWords is the panicking convenience form; tests use bytesToWordsErr
// to assert the error path without recovering panics.
var _ = binary.BigEndian
