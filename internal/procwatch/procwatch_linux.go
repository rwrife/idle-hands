//go:build linux

package procwatch

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// clockTicksPerSec is the kernel USER_HZ used to convert the jiffie counters in
// /proc/<pid>/stat to real time. It is fixed at 100 on effectively all Linux
// builds (CONFIG_HZ=100 for userspace accounting), and there is no portable
// libc-free way to read sysconf(_SC_CLK_TCK) from pure Go, so we assume 100. A
// wrong value would only scale the CPU delta uniformly; since Active is decided
// by a ratio against the same clock, the classification is unaffected in
// practice.
const clockTicksPerSec = 100

// procSampler samples CPU time for a specific pid by reading /proc/<pid>/stat.
type procSampler struct {
	pid   int
	name  string // the resolved process name, for Describe
	label string // cached Describe() string
}

// NewNameSampler resolves the first process whose name matches `name` and
// returns a Sampler for it. Matching is done against the process's comm (the
// 15-char kernel-truncated name in /proc/<pid>/stat) and, as a fallback, the
// basename of its executable from /proc/<pid>/cmdline, so both `code` and a
// longer `Code Helper (Renderer)` style argv[0] can be targeted. The current
// process and kernel threads are skipped. When several processes match, the one
// with the most CPU time so far is chosen (heuristically the "main" one rather
// than a helper), which keeps the target stable for GUI apps that spawn a tree
// of helpers.
func NewNameSampler(name string) (Sampler, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, fmt.Errorf("procwatch: empty process name")
	}

	self := os.Getpid()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("procwatch: read /proc: %w", err)
	}

	bestPID := -1
	var bestName string
	var bestCPU time.Duration
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue // not a pid dir, or ourselves
		}
		comm, cpu, ok := readStat(pid)
		if !ok {
			continue // vanished between readdir and read, or a kernel thread
		}
		if !nameMatches(pid, comm, trimmed) {
			continue
		}
		if bestPID == -1 || cpu > bestCPU {
			bestPID, bestName, bestCPU = pid, comm, cpu
		}
	}
	if bestPID == -1 {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, trimmed)
	}
	return &procSampler{
		pid:   bestPID,
		name:  bestName,
		label: fmt.Sprintf("pid %d (%s)", bestPID, bestName),
	}, nil
}

// Sample reads the target pid's cumulative CPU time. A gone process (its
// /proc/<pid>/stat no longer readable) is reported as Alive=false with a nil
// error so the poller can cleanly report Exited. Other read errors are returned
// so the poller can treat them as a skipped tick.
func (p *procSampler) Sample() (Sample, error) {
	_, cpu, ok := readStat(p.pid)
	if !ok {
		// Distinguish "process gone" (the common, expected end state) from a
		// real read error by probing the directory. If the dir is gone, the
		// process exited; otherwise surface the error.
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(p.pid))); os.IsNotExist(err) {
			return Sample{Alive: false}, nil
		}
		return Sample{}, fmt.Errorf("procwatch: read stat for pid %d failed", p.pid)
	}
	return Sample{CPU: cpu, Alive: true}, nil
}

// Describe returns the cached "pid N (name)" label.
func (p *procSampler) Describe() string { return p.label }

// readStat parses /proc/<pid>/stat and returns the process comm and its
// cumulative CPU time (utime+stime), plus ok=false if the file can't be read or
// parsed. The comm field is parenthesized and may itself contain spaces and
// parentheses (e.g. "(Web Content)"), so we split on the *last* ')' to find the
// stable, space-separated remainder where utime (field 14) and stime (field 15)
// live — the classic robust way to parse this file.
func readStat(pid int) (comm string, cpu time.Duration, ok bool) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", 0, false
	}
	s := string(data)
	open := strings.IndexByte(s, '(')
	close := strings.LastIndexByte(s, ')')
	if open < 0 || close < 0 || close < open {
		return "", 0, false
	}
	comm = s[open+1 : close]

	// Fields after comm are space separated. In the full stat line these are
	// fields 3.. (1=pid, 2=comm). utime is field 14 and stime is field 15, i.e.
	// indices 11 and 12 in this post-comm slice (field 3 == index 0).
	rest := strings.Fields(s[close+1:])
	const utimeIdx, stimeIdx = 11, 12
	if len(rest) <= stimeIdx {
		return "", 0, false
	}
	utime, err1 := strconv.ParseInt(rest[utimeIdx], 10, 64)
	stime, err2 := strconv.ParseInt(rest[stimeIdx], 10, 64)
	if err1 != nil || err2 != nil {
		return "", 0, false
	}
	ticks := utime + stime
	cpu = time.Duration(ticks) * time.Second / clockTicksPerSec
	return comm, cpu, true
}

// nameMatches reports whether the process (by its comm and cmdline) matches the
// user's requested name. It matches case-insensitively against the comm and,
// failing that, the basename of argv[0] from cmdline, so both the kernel comm
// and a longer executable name work as targets. The comparison is an exact
// (case-insensitive) match on the basename to avoid a bare "co" matching
// "code"; users pass the actual process name.
func nameMatches(pid int, comm, want string) bool {
	if strings.EqualFold(comm, want) {
		return true
	}
	// Fall back to argv[0]'s basename from cmdline (NUL-separated).
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || len(data) == 0 {
		return false
	}
	argv0 := data
	if i := strings.IndexByte(string(data), 0); i >= 0 {
		argv0 = data[:i]
	}
	base := filepath.Base(strings.TrimSpace(string(argv0)))
	return strings.EqualFold(base, want)
}
