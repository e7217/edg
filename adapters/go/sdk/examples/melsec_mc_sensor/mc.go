package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// MC protocol (MELSEC Communication), 3E frame, binary code -- the frame a
// MELSEC Q/L/iQ-R CPU or Ethernet module answers on its "MC protocol" port.
// Only batch read in word units (command 0401, subcommand 0000) is used: it
// reads word devices directly and bit devices sixteen at a time.

// Device is a parsed device address such as D100, M8000 or X1F.
type Device struct {
	Name   string // canonical: D100, X1F
	Code   byte   // binary device code
	Number uint32
	Bit    bool // a bit device, read sixteen at a time
}

// deviceCodes maps a device letter to its binary code and whether its number
// is written in hexadecimal. X, Y, B and W are hexadecimal on MELSEC.
var deviceCodes = map[string]struct {
	code byte
	hex  bool
	bit  bool
}{
	"D":  {0xA8, false, false},
	"W":  {0xB4, true, false},
	"R":  {0xAF, false, false},
	"ZR": {0xB0, false, false},
	"M":  {0x90, false, true},
	"L":  {0x92, false, true},
	"B":  {0xA0, true, true},
	"X":  {0x9C, true, true},
	"Y":  {0x9D, true, true},
}

// ParseDevice parses "D100", "ZR2000" or "X1F".
func ParseDevice(s string) (Device, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	for _, prefix := range []string{"ZR", "D", "W", "R", "M", "L", "B", "X", "Y"} {
		if !strings.HasPrefix(s, prefix) {
			continue
		}
		info := deviceCodes[prefix]
		digits := s[len(prefix):]
		base := 10
		if info.hex {
			base = 16
		}
		n, err := strconv.ParseUint(digits, base, 24)
		if digits == "" || err != nil {
			kind := "decimal"
			if info.hex {
				kind = "hexadecimal"
			}
			return Device{}, fmt.Errorf("device %q: %s number expected after %s", s, kind, prefix)
		}
		return Device{Name: s, Code: info.code, Number: uint32(n), Bit: info.bit}, nil
	}
	return Device{}, fmt.Errorf("device %q: unsupported device (D, W, R, ZR, M, L, B, X, Y)", s)
}

// MCClient speaks the 3E binary frame over one TCP connection.
type MCClient struct {
	conn    net.Conn
	timeout time.Duration
}

// DialMC opens a connection to a PLC's MC protocol port.
func DialMC(addr string, timeout time.Duration) (*MCClient, error) {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	return &MCClient{conn: c, timeout: timeout}, nil
}

func (c *MCClient) Close() error { return c.conn.Close() }

// ErrEndCode is a PLC-reported error: the request was understood and refused.
type ErrEndCode uint16

func (e ErrEndCode) Error() string {
	return fmt.Sprintf("PLC end code 0x%04X", uint16(e))
}

// ReadWords reads count words starting at dev.
func (c *MCClient) ReadWords(dev Device, count uint16) ([]uint16, error) {
	if count == 0 || count > 960 {
		return nil, fmt.Errorf("word count %d out of range 1..960", count)
	}
	req := make([]byte, 0, 21)
	req = append(req, 0x50, 0x00) // subheader
	req = append(req, 0x00)       // network number
	req = append(req, 0xFF)       // PC number
	req = binary.LittleEndian.AppendUint16(req, 0x03FF)
	req = append(req, 0x00)                             // station
	req = binary.LittleEndian.AppendUint16(req, 12)     // length: timer .. end
	req = binary.LittleEndian.AppendUint16(req, 0x0010) // monitoring timer, 4 s in 250 ms units
	req = binary.LittleEndian.AppendUint16(req, 0x0401) // batch read
	req = binary.LittleEndian.AppendUint16(req, 0x0000) // word units
	req = append(req, byte(dev.Number), byte(dev.Number>>8), byte(dev.Number>>16))
	req = append(req, dev.Code)
	req = binary.LittleEndian.AppendUint16(req, count)

	_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
	if _, err := c.conn.Write(req); err != nil {
		return nil, err
	}
	head := make([]byte, 11)
	if _, err := io.ReadFull(c.conn, head); err != nil {
		return nil, err
	}
	if head[0] != 0xD0 || head[1] != 0x00 {
		return nil, fmt.Errorf("unexpected response subheader %02X%02X", head[0], head[1])
	}
	n := binary.LittleEndian.Uint16(head[7:9]) // end code + data
	if n < 2 {
		return nil, errors.New("response shorter than its end code")
	}
	end := binary.LittleEndian.Uint16(head[9:11])
	body := make([]byte, n-2)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return nil, err
	}
	if end != 0 {
		return nil, ErrEndCode(end)
	}
	if len(body) != 2*int(count) {
		return nil, fmt.Errorf("asked for %d words, got %d bytes", count, len(body))
	}
	words := make([]uint16, count)
	for i := range words {
		words[i] = binary.LittleEndian.Uint16(body[2*i:])
	}
	return words, nil
}
