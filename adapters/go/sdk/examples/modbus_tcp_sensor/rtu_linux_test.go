//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/e7217/edg/adapters/go/sdk"
)

// openPTY returns the master side of a pseudo-terminal and the path of its
// slave. The adapter opens the slave as its serial port; the test answers on
// the master as the Modbus slave would.
func openPTY(t *testing.T) (*os.File, string) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	unlock := 0
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Fatalf("unlockpt: %v", e)
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		t.Fatalf("ptsname: %v", e)
	}
	return m, fmt.Sprintf("/dev/pts/%d", n)
}

func crc16(b []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, x := range b {
		crc ^= uint16(x)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// serveRTU answers read-holding/read-input requests for unit from regs until
// the pty closes. It is the smallest Modbus RTU slave that proves the adapter
// frames requests correctly: a wrong CRC, unit or length gets no answer, and
// the adapter times out.
func serveRTU(m *os.File, unit byte, regs map[uint16]uint16) {
	buf := make([]byte, 0, 256)
	chunk := make([]byte, 256)
	for {
		n, err := m.Read(chunk)
		if err != nil {
			return
		}
		buf = append(buf, chunk[:n]...)
		for len(buf) >= 8 {
			req := buf[:8]
			if crc16(req[:6]) != binary.LittleEndian.Uint16(req[6:]) || req[0] != unit || (req[1] != 3 && req[1] != 4) {
				buf = buf[1:] // resynchronise
				continue
			}
			addr := binary.BigEndian.Uint16(req[2:])
			count := binary.BigEndian.Uint16(req[4:])
			resp := []byte{unit, req[1], byte(2 * count)}
			for i := uint16(0); i < count; i++ {
				resp = binary.BigEndian.AppendUint16(resp, regs[addr+i])
			}
			resp = binary.LittleEndian.AppendUint16(resp, crc16(resp))
			if _, err := m.Write(resp); err != nil {
				return
			}
			buf = buf[8:]
		}
	}
}

func TestRTUTransportReadsRegisters(t *testing.T) {
	master, port := openPTY(t)
	go serveRTU(master, 7, map[uint16]uint16{0: 235, 10: 0x4148, 11: 0x0000}) // 23.5 raw, 12.5 as float32

	cfg := &ModbusConfig{
		Transport: TransportRTU,
		Serial:    SerialConfig{Port: port},
		UnitID:    7,
		Timeout:   1,
		Registers: []RegisterSpec{
			{Name: "temperature", Function: "holding", Address: 0, Type: "int16", WordOrder: "ABCD", Scale: 0.1},
			{Name: "flow", Function: "input", Address: 10, Type: "float32", WordOrder: "ABCD", Scale: 1},
		},
	}
	if err := cfg.Serial.applyDefaults(); err != nil {
		t.Fatal(err)
	}
	d := NewModbusDevice(cfg)
	ctx := context.Background()
	if err := d.ConnectDevice(ctx); err != nil {
		t.Fatal(err)
	}
	defer d.DisconnectDevice(ctx)

	got, err := d.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || *got[0].Number != 23.5 || *got[1].Number != 12.5 {
		t.Fatalf("got %+v", got)
	}
}

func TestLoadConfigRTU(t *testing.T) {
	path := t.TempDir() + "/m.yaml"
	if err := os.WriteFile(path, []byte("transport: rtu\nserial:\n  port: /dev/ttyUSB0\n  parity: N\n  stop_bits: 2\nunit_id: 3\nasset_id: kiln-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Serial.BaudRate != 9600 || cfg.Serial.DataBits != 8 || cfg.Serial.Parity != "N" || cfg.Serial.StopBits != 2 {
		t.Fatalf("serial = %+v", cfg.Serial)
	}
	if cfg.Protocol() != ProtocolModbusRTU {
		t.Fatalf("protocol = %q", cfg.Protocol())
	}
	for want, body := range map[string]string{
		"serial.port":   "transport: rtu\nregisters: [{name: a, function: holding, address: 0, type: int16}]\n",
		"not supported": "transport: udp\nhost: x\nregisters: [{name: a, function: holding, address: 0, type: int16}]\n",
		"serial.parity": "transport: rtu\nserial: {port: /dev/x, parity: M}\nasset_id: a\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

func TestRegistersFromPointsMatchesTheTransport(t *testing.T) {
	cfg := &ModbusConfig{Transport: TransportRTU}
	if _, err := registersFromPoints(&sdk.PointList{Protocol: ProtocolModbusTCP}, cfg.Protocol()); err == nil {
		t.Fatal("an RTU adapter accepted a modbus-tcp point list")
	}
}
