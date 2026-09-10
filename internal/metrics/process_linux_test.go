//go:build linux

package metrics

import (
	"strings"
	"testing"
)

// A real line from /proc/self/stat. The comm field deliberately contains a
// space and a closing parenthesis, which is what breaks the naive
// strings.Fields parse.
const statFixture = `1234 (edg core) R 1 1234 1234 0 -1 4194304 3210 0 0 0 ` +
	`731 219 0 0 20 0 14 0 987654 2818572288 4271 18446744073709551615 ` +
	`4194304 12345678 140737488346848 0 0 0 0 0 0 0 0 0 17 3 0 0 0 0 0`

func TestParseProcStat(t *testing.T) {
	st, ok := parseProcStat(statFixture)
	if !ok {
		t.Fatal("parse failed")
	}
	if st.utimeTicks != 731 {
		t.Errorf("utime = %d, want 731", st.utimeTicks)
	}
	if st.stimeTicks != 219 {
		t.Errorf("stime = %d, want 219", st.stimeTicks)
	}
	if st.startTimeTicks != 987654 {
		t.Errorf("starttime = %d, want 987654", st.startTimeTicks)
	}
	if st.vsizeBytes != 2818572288 {
		t.Errorf("vsize = %d, want 2818572288", st.vsizeBytes)
	}
	if st.rssPages != 4271 {
		t.Errorf("rss = %d, want 4271", st.rssPages)
	}
}

func TestParseProcStatRejectsGarbage(t *testing.T) {
	for name, in := range map[string]string{
		"empty":       "",
		"no comm":     "1234 R 1 1 1",
		"truncated":   "1234 (edg) R 1 2 3",
		"non-numeric": strings.Replace(statFixture, " 731 ", " abc ", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := parseProcStat(in); ok {
				t.Error("accepted a malformed stat line")
			}
		})
	}
}

func TestParseBootTime(t *testing.T) {
	content := "cpu  1 2 3\nintr 9\nbtime 1757000000\nprocesses 42\n"
	got, ok := parseBootTime(content)
	if !ok || got != 1757000000 {
		t.Errorf("parseBootTime = %v, %v; want 1757000000, true", got, ok)
	}
	if _, ok := parseBootTime("cpu 1 2 3\n"); ok {
		t.Error("accepted /proc/stat with no btime line")
	}
	if _, ok := parseBootTime("btime notanumber\n"); ok {
		t.Error("accepted a non-numeric btime")
	}
}

func TestRegisterProcess(t *testing.T) {
	r := NewRegistry()
	r.RegisterProcess()
	out := string(r.Gather())

	for _, name := range []string{
		"process_cpu_seconds_total",
		"process_resident_memory_bytes",
		"process_virtual_memory_bytes",
		"process_start_time_seconds",
		"process_open_fds",
		"process_max_fds",
		"process_virtual_memory_max_bytes",
	} {
		if !strings.Contains(out, name+" ") {
			t.Errorf("%s missing from the exposition:\n%s", name, out)
		}
	}

	if rss := valueOf(t, r.Gather(), "process_resident_memory_bytes"); rss <= 0 {
		t.Errorf("process_resident_memory_bytes = %v, want > 0", rss)
	}
	if fds := valueOf(t, r.Gather(), "process_open_fds"); fds <= 0 {
		t.Errorf("process_open_fds = %v, want > 0", fds)
	}
	if start := valueOf(t, r.Gather(), "process_start_time_seconds"); start < 1_500_000_000 {
		t.Errorf("process_start_time_seconds = %v, want a plausible unix time", start)
	}
}
