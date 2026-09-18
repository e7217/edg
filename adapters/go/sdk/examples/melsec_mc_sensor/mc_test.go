package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/e7217/edg/adapters/go/sdk"
)

// fakePLC answers MC protocol 3E binary batch-read requests from a device
// memory keyed by (device code, number). It checks every fixed byte of the
// request, so a framing mistake is a failed read rather than a lucky answer.
type fakePLC struct {
	t      *testing.T
	mem    map[[2]uint32]uint16
	refuse map[[2]uint32]uint16 // end code to return for a head device
	addr   string
}

func startFakePLC(t *testing.T) *fakePLC {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	p := &fakePLC{t: t, mem: map[[2]uint32]uint16{}, refuse: map[[2]uint32]uint16{}, addr: l.Addr().String()}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go p.serve(c)
		}
	}()
	return p
}

func (p *fakePLC) set(code byte, n uint32, words ...uint16) {
	for i, w := range words {
		p.mem[[2]uint32{uint32(code), n + uint32(i)}] = w
	}
}

func (p *fakePLC) serve(c net.Conn) {
	defer c.Close()
	for {
		req := make([]byte, 21)
		if _, err := io.ReadFull(c, req); err != nil {
			return
		}
		want := []byte{0x50, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x00, 12, 0, 0x10, 0x00, 0x01, 0x04, 0x00, 0x00}
		for i, b := range want {
			if req[i] != b {
				p.t.Errorf("request byte %d = %#02x, want %#02x (request % X)", i, req[i], b, req)
				return
			}
		}
		n := uint32(req[15]) | uint32(req[16])<<8 | uint32(req[17])<<16
		code := req[18]
		count := binary.LittleEndian.Uint16(req[19:])
		resp := []byte{0xD0, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x00}
		if end, ok := p.refuse[[2]uint32{uint32(code), n}]; ok {
			resp = binary.LittleEndian.AppendUint16(resp, 2+9)
			resp = binary.LittleEndian.AppendUint16(resp, end)
			resp = append(resp, make([]byte, 9)...) // error information block
		} else {
			resp = binary.LittleEndian.AppendUint16(resp, 2+2*count)
			resp = binary.LittleEndian.AppendUint16(resp, 0)
			for i := uint16(0); i < count; i++ {
				resp = binary.LittleEndian.AppendUint16(resp, p.mem[[2]uint32{uint32(code), n + uint32(i)}])
			}
		}
		if _, err := c.Write(resp); err != nil {
			return
		}
	}
}

func TestParseDevice(t *testing.T) {
	for in, want := range map[string]Device{
		"D100":   {Name: "D100", Code: 0xA8, Number: 100},
		"d100":   {Name: "D100", Code: 0xA8, Number: 100},
		"ZR2000": {Name: "ZR2000", Code: 0xB0, Number: 2000},
		"X1F":    {Name: "X1F", Code: 0x9C, Number: 0x1F, Bit: true},
		"W10":    {Name: "W10", Code: 0xB4, Number: 0x10},
		"M8000":  {Name: "M8000", Code: 0x90, Number: 8000, Bit: true},
	} {
		got, err := ParseDevice(in)
		if err != nil || got != want {
			t.Errorf("%s: got %+v, %v; want %+v", in, got, err, want)
		}
	}
	for in, want := range map[string]string{
		"D":     "decimal number expected",
		"D1A":   "decimal number expected",
		"XG":    "hexadecimal number expected",
		"S10":   "unsupported device",
		"40001": "unsupported device",
	} {
		if _, err := ParseDevice(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", in, err, want)
		}
	}
}

func TestPLCReadsAndDecodes(t *testing.T) {
	plc := startFakePLC(t)
	plc.set(0xA8, 100, uint16(0xFF15)) // D100 = -235
	plc.set(0xA8, 200, 0x86A0, 0x0001) // D200/D201 = 100000, low word first
	f := math.Float32bits(12.5)
	plc.set(0xA8, 210, uint16(f), uint16(f>>16)) // D210/D211 = 12.5
	plc.set(0x90, 100, 0x0001)                   // M100 on
	plc.set(0xA8, 300, 1<<4)                     // D300 bit 4
	plc.refuse[[2]uint32{0xA8, 999}] = 0xC056    // out of range

	host, port, _ := net.SplitHostPort(plc.addr)
	portN, _ := strconv.Atoi(port)
	cfg := &Config{Host: host, Port: portN, Timeout: 2}
	specs, err := specsFrom([]PointConfig{
		{Name: "temperature", Address: "D100", Type: "int16", Scale: 0.1, Unit: "°C"},
		{Name: "counter", Address: "D200", Type: "int32"},
		{Name: "flow", Address: "D210", Type: "float32"},
		{Name: "running", Address: "M100"},
		{Name: "alarm", Address: "D300.4"},
		{Name: "missing", Address: "D999"},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := NewPLC(cfg, specs)
	ctx := context.Background()
	if err := p.ConnectDevice(ctx); err != nil {
		t.Fatal(err)
	}
	defer p.DisconnectDevice(ctx)

	got, err := p.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]sdk.TagValue{}
	for _, v := range got {
		by[v.Name] = v
	}
	if len(got) != 5 {
		t.Fatalf("got %d values, want 5 (a refused device is skipped, not fatal): %+v", len(got), got)
	}
	if v := by["temperature"]; *v.Number != -23.5 || v.Unit != "°C" {
		t.Errorf("temperature = %+v", v)
	}
	if v := by["counter"]; *v.Number != 100000 {
		t.Errorf("counter = %v", *v.Number)
	}
	if v := by["flow"]; *v.Number != 12.5 {
		t.Errorf("flow = %v", *v.Number)
	}
	if v := by["running"]; v.Flag == nil || !*v.Flag {
		t.Errorf("running = %+v", v)
	}
	if v := by["alarm"]; v.Flag == nil || !*v.Flag {
		t.Errorf("alarm = %+v", v)
	}
}

func TestEndCodeIsReported(t *testing.T) {
	plc := startFakePLC(t)
	plc.refuse[[2]uint32{0xA8, 5}] = 0xC051
	c, err := DialMC(plc.addr, 2e9)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.ReadWords(Device{Code: 0xA8, Number: 5}, 1)
	var end ErrEndCode
	if !errors.As(err, &end) || end != 0xC051 {
		t.Fatalf("err = %v", err)
	}
	// The connection stays usable after an end code.
	plc.set(0xA8, 6, 42)
	w, err := c.ReadWords(Device{Code: 0xA8, Number: 6}, 1)
	if err != nil || w[0] != 42 {
		t.Fatalf("after end code: %v %v", w, err)
	}
}

func TestSpecValidation(t *testing.T) {
	for want, p := range map[string]PointConfig{
		"already a bit device":   {Name: "p", Address: "M100.1"},
		"reads as type bit":      {Name: "p", Address: "M100", Type: "int16"},
		"bit index after '.'":    {Name: "p", Address: "D100.16"},
		"type \"int64\" not sup": {Name: "p", Address: "D100", Type: "int64"},
	} {
		if _, err := specsFrom([]PointConfig{p}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

func TestSpecsFromPointList(t *testing.T) {
	specs, err := specsFromPointList(&sdk.PointList{Protocol: ProtocolMELSEC, Points: []sdk.Point{
		{Name: "temperature", Address: "D100", Unit: "°C", Enabled: true, Encoding: map[string]any{"type": "int16", "scale": 0.1}},
		{Name: "running", Address: "M100", Enabled: true},
		{Name: "old", Address: "D1", Enabled: false},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 || specs[0].Scale != 0.1 || specs[1].Type != "bit" {
		t.Fatalf("specs = %+v", specs)
	}
	if _, err := specsFromPointList(&sdk.PointList{Protocol: "modbus-tcp"}); err == nil {
		t.Fatal("accepted a modbus-tcp list")
	}
}

// The request for "read one word at D100" as published in the MC protocol
// reference (3E frame, binary). The fake PLC above shares this code's reading
// of the specification; this pins it to the published bytes instead.
func TestRequestMatchesThePublishedFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	c := &MCClient{conn: client, timeout: 2e9}
	go func() { _, _ = c.ReadWords(Device{Code: 0xA8, Number: 100}, 1) }()
	got := make([]byte, 21)
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatal(err)
	}
	const want = "500000FFFF03000C00100001040000640000A80100"
	if hex := strings.ToUpper(hexString(got)); hex != want {
		t.Fatalf("request %s\n    want %s", hex, want)
	}
}

func hexString(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, x := range b {
		out = append(out, digits[x>>4], digits[x&15])
	}
	return string(out)
}
