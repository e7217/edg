package metrics

import (
	"math"
	"strconv"
)

// ContentType is the Prometheus text exposition format 0.0.4 media type.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

func writeLabelledSample(buf *[]byte, name, label, value string, n int64) {
	*buf = append(*buf, name...)
	*buf = append(*buf, '{')
	*buf = append(*buf, label...)
	*buf = append(*buf, '=', '"')
	*buf = appendEscapedLabelValue(*buf, value)
	*buf = append(*buf, '"', '}', ' ')
	*buf = appendInt(*buf, n)
	*buf = append(*buf, '\n')
}

// appendEscapedHelp escapes a HELP string. The exposition format escapes only
// backslash and newline here -- a double quote in HELP is literal, unlike in a
// label value.
func appendEscapedHelp(buf []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			buf = append(buf, '\\', '\\')
		case '\n':
			buf = append(buf, '\\', 'n')
		default:
			buf = append(buf, c)
		}
	}
	return buf
}

// appendEscapedLabelValue escapes a label value: backslash, double quote and
// newline.
func appendEscapedLabelValue(buf []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			buf = append(buf, '\\', '\\')
		case '"':
			buf = append(buf, '\\', '"')
		case '\n':
			buf = append(buf, '\\', 'n')
		default:
			buf = append(buf, c)
		}
	}
	return buf
}

func appendInt(buf []byte, v int64) []byte   { return strconv.AppendInt(buf, v, 10) }
func appendUint(buf []byte, v uint64) []byte { return strconv.AppendUint(buf, v, 10) }

// appendFloat renders a float the way Prometheus expects: whole numbers without
// a decimal point, infinities as +Inf/-Inf, and NaN as NaN.
func appendFloat(buf []byte, v float64) []byte {
	switch {
	case math.IsInf(v, 1):
		return append(buf, "+Inf"...)
	case math.IsInf(v, -1):
		return append(buf, "-Inf"...)
	case math.IsNaN(v):
		return append(buf, "NaN"...)
	case v == math.Trunc(v) && math.Abs(v) < 1e15:
		return strconv.AppendInt(buf, int64(v), 10)
	default:
		return strconv.AppendFloat(buf, v, 'g', -1, 64)
	}
}
