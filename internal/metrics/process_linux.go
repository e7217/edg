//go:build linux

package metrics

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// clockTicksPerSecond is _SC_CLK_TCK. It is 100 on every Linux port Go
// supports, and reading the real value needs cgo, which this binary does not
// use. If it were ever wrong, process_cpu_seconds_total would be off by a
// constant factor -- visible immediately as a CPU rate above the core count.
const clockTicksPerSecond = 100

// procStat is the subset of /proc/self/stat this process reports.
type procStat struct {
	utimeTicks     uint64
	stimeTicks     uint64
	startTimeTicks uint64
	vsizeBytes     uint64
	rssPages       uint64
}

// parseProcStat reads /proc/<pid>/stat.
//
// The comm field is parenthesised and may itself contain spaces and
// parentheses, so everything before the LAST ')' is skipped rather than
// splitting the whole line on whitespace.
func parseProcStat(content string) (procStat, bool) {
	end := strings.LastIndexByte(content, ')')
	if end < 0 || end+2 > len(content) {
		return procStat{}, false
	}
	// fields[0] is the state field, i.e. field 3 of the documented layout, so
	// documented field N lives at fields[N-3].
	fields := strings.Fields(content[end+2:])
	const (
		utime     = 14 - 3
		stime     = 15 - 3
		startTime = 22 - 3
		vsize     = 23 - 3
		rss       = 24 - 3
	)
	if len(fields) <= rss {
		return procStat{}, false
	}
	var st procStat
	var err error
	if st.utimeTicks, err = strconv.ParseUint(fields[utime], 10, 64); err != nil {
		return procStat{}, false
	}
	if st.stimeTicks, err = strconv.ParseUint(fields[stime], 10, 64); err != nil {
		return procStat{}, false
	}
	if st.startTimeTicks, err = strconv.ParseUint(fields[startTime], 10, 64); err != nil {
		return procStat{}, false
	}
	if st.vsizeBytes, err = strconv.ParseUint(fields[vsize], 10, 64); err != nil {
		return procStat{}, false
	}
	if st.rssPages, err = strconv.ParseUint(fields[rss], 10, 64); err != nil {
		return procStat{}, false
	}
	return st, true
}

// parseBootTime reads the btime line of /proc/stat: seconds since the epoch at
// which the machine booted. /proc/self/stat reports the process start as ticks
// since boot, so this is what turns it into a wall-clock timestamp.
func parseBootTime(content string) (float64, bool) {
	for _, line := range strings.Split(content, "\n") {
		rest, ok := strings.CutPrefix(line, "btime ")
		if !ok {
			continue
		}
		v, err := strconv.ParseUint(strings.TrimSpace(rest), 10, 64)
		if err != nil {
			return 0, false
		}
		return float64(v), true
	}
	return 0, false
}

// procSampler caches one /proc read per scrape.
type procSampler struct {
	mu       sync.Mutex
	stat     procStat
	ok       bool
	openFDs  float64
	bootTime float64
	pageSize uint64
}

func (p *procSampler) refresh() {
	content, err := os.ReadFile("/proc/self/stat")
	stat, ok := procStat{}, false
	if err == nil {
		stat, ok = parseProcStat(string(content))
	}

	// Counting the directory costs an fd of its own, which os.ReadDir has
	// already closed by the time the slice is returned -- but it was open
	// while the directory was read, so this can overcount by one. Everyone
	// exposing process_open_fds has the same caveat.
	fds := float64(0)
	if entries, err := os.ReadDir("/proc/self/fd"); err == nil {
		fds = float64(len(entries))
	}

	p.mu.Lock()
	p.stat, p.ok, p.openFDs = stat, ok, fds
	p.mu.Unlock()
}

func (p *procSampler) read(fn func(procStat) float64) float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.ok {
		return 0
	}
	return fn(p.stat)
}

// registerProcess adds the process_* family from /proc. On Linux this is the
// only source for resident memory and open file descriptors -- the two numbers
// that actually kill a small edge box.
func (r *Registry) registerProcess() {
	p := &procSampler{pageSize: uint64(os.Getpagesize())}
	if content, err := os.ReadFile("/proc/stat"); err == nil {
		p.bootTime, _ = parseBootTime(string(content))
	}
	p.refresh()
	r.BeforeGather(p.refresh)

	r.NewFuncCounter(Desc{
		Name: "process_cpu_seconds_total",
		Help: "User plus system CPU seconds consumed by this process.",
	}, func() float64 {
		return p.read(func(s procStat) float64 {
			return float64(s.utimeTicks+s.stimeTicks) / clockTicksPerSecond
		})
	})

	r.NewFuncGauge(Desc{
		Name: "process_resident_memory_bytes",
		Help: "Resident set size. On a 512 MB gateway this is the number that decides whether the OOM killer arrives.",
	}, func() float64 {
		return p.read(func(s procStat) float64 { return float64(s.rssPages * p.pageSize) })
	})

	r.NewFuncGauge(Desc{
		Name: "process_virtual_memory_bytes",
		Help: "Virtual address space size.",
	}, func() float64 {
		return p.read(func(s procStat) float64 { return float64(s.vsizeBytes) })
	})

	if p.bootTime > 0 {
		r.NewFuncGauge(Desc{
			Name: "process_start_time_seconds",
			Help: "Unix time at which this process started. Its changing is the restart signal.",
		}, func() float64 {
			return p.read(func(s procStat) float64 {
				return p.bootTime + float64(s.startTimeTicks)/clockTicksPerSecond
			})
		})
	}

	r.NewFuncGauge(Desc{
		Name: "process_open_fds",
		Help: "Open file descriptors. Against process_max_fds this is the second way a long-lived gateway dies.",
	}, func() float64 {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.openFDs
	})

	r.NewFuncGauge(Desc{
		Name: "process_max_fds",
		Help: "Soft RLIMIT_NOFILE.",
	}, func() float64 { return rlimitSoft(syscall.RLIMIT_NOFILE) })

	r.NewFuncGauge(Desc{
		Name: "process_virtual_memory_max_bytes",
		Help: "Soft RLIMIT_AS. -1 when unlimited.",
	}, func() float64 { return rlimitSoft(syscall.RLIMIT_AS) })
}

// rlimitSoft reports a soft resource limit, or -1 when it is unlimited --
// the convention every process_* exporter uses, since RLIM_INFINITY rendered
// as a number would look like a real 18-exabyte ceiling.
func rlimitSoft(resource int) float64 {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(resource, &lim); err != nil {
		return 0
	}
	if lim.Cur == ^uint64(0) {
		return -1
	}
	return float64(lim.Cur)
}
