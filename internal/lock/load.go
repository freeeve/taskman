package lock

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A lock excludes the processes that ask for it. Nothing asks a VM, a stray
// daemon, or a sibling's release build to wait, and those are what actually
// ruin a measurement: a run can hold the resource and still be timed on a
// machine at load 11. So a caller may also gate on how busy the machine is --
// before the work starts, and, for a wrapped command, for as long as it runs.
//
// Load is measured in cores busy with FOREIGN work: everything outside the
// process tree being timed. A 12-thread benchmark legitimately drives the load
// average to 12, so total load says nothing about whether a run had the machine
// to itself; foreign load says exactly that, and the pre-flight and in-flight
// checks then speak the same unit.

// quietPoll is how often a pre-flight gate rechecks a busy machine.
const quietPoll = 2 * time.Second

// Proc is a process holding CPU, named when a gate refuses or a run is judged
// contaminated -- a threshold that will not say who broke it is a threshold
// nobody can act on.
type Proc struct {
	PID  int
	CPU  float64 // cores, not percent
	Comm string
}

// String renders a process for an error message: "pebble_updater (pid 87881,
// 0.8 cores)".
func (p Proc) String() string {
	return fmt.Sprintf("%s (pid %d, %.1f cores)", p.Comm, p.PID, p.CPU)
}

// LoadedError reports a machine too busy for a timed run, and who is making it
// busy.
type LoadedError struct {
	Load   float64 // foreign cores busy
	Max    float64
	Waited time.Duration
	Top    []Proc
}

func (e *LoadedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "machine is busy: %.1f cores of other work, over the %.1f allowed", e.Load, e.Max)
	if e.Waited > 0 {
		fmt.Fprintf(&b, " (waited %s for it to settle)", e.Waited)
	}
	for _, p := range e.Top {
		fmt.Fprintf(&b, "\n  %s", p)
	}
	return b.String()
}

// ForeignLoad returns the cores busy with work outside the given process tree
// (pass 0 to count everything), and every process contributing, busiest first.
//
// It reads ps rather than the load average on purpose: load1 is a decaying
// one-minute mean, so it lags a machine that just went quiet and -- fatally for
// the in-flight check -- cannot tell the timed command's own CPU from a
// stranger's.
func ForeignLoad(excludeTree int) (float64, []Proc, error) {
	out, err := exec.Command("ps", "-Ao", "pid=,ppid=,pcpu=,comm=").Output()
	if err != nil {
		return 0, nil, fmt.Errorf("reading process table: %w", err)
	}
	type row struct {
		ppid int
		cpu  float64
		comm string
	}
	rows := map[int]row{}
	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		cpu, err3 := strconv.ParseFloat(f[2], 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		rows[pid] = row{ppid: ppid, cpu: cpu, comm: comm(strings.Join(f[3:], " "))}
	}
	// The timed command's own threads are not contamination; neither is the
	// taskman wrapper watching it.
	ours := func(pid int) bool {
		for range len(rows) { // bounded: a ppid cycle must not hang the walk
			if pid == 0 || pid == 1 {
				return false
			}
			if pid == excludeTree {
				return true
			}
			r, ok := rows[pid]
			if !ok {
				return false
			}
			pid = r.ppid
		}
		return false
	}
	var busy float64
	var procs []Proc
	for pid, r := range rows {
		if excludeTree != 0 && ours(pid) {
			continue
		}
		cores := r.cpu / 100
		busy += cores
		if cores > 0 {
			procs = append(procs, Proc{PID: pid, CPU: cores, Comm: r.comm})
		}
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].CPU > procs[j].CPU })
	return busy, procs, nil
}

// worst is the handful of processes worth naming in an error: a wall of idle
// pids helps nobody.
func worst(procs []Proc) []Proc {
	if len(procs) > 3 {
		return procs[:3]
	}
	return procs
}

// comm shortens a ps command to its basename, so a rustc under a long toolchain
// path is still readable in an error.
func comm(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 && i+1 < len(s) {
		return s[i+1:]
	}
	return s
}

// WaitForQuiet blocks until foreign work drops to max cores or below, giving up
// after wait. It is the pre-flight gate: the caller holds the resource by now,
// which stops any *cooperating* load, and this waits out the rest -- a sibling's
// build finishing, a VM settling -- rather than timing a run against it.
func WaitForQuiet(max float64, wait time.Duration) error {
	started := time.Now()
	deadline := started.Add(wait)
	for {
		busy, top, err := ForeignLoad(os.Getpid())
		if err != nil {
			return err
		}
		if busy <= max {
			return nil
		}
		if time.Now().After(deadline) {
			return &LoadedError{Load: busy, Max: max, Waited: time.Since(started).Round(time.Second), Top: worst(top)}
		}
		time.Sleep(quietPoll)
	}
}

// Watch samples foreign load until done closes, and reports the mean and peak
// cores of other work seen across the run, plus the worst offender.
//
// The verdict rests on the mean, not the peak: a 5-second compile that blips
// during a 40-minute sweep is noise, while a daemon eating a core throughout is
// exactly the contamination that has published bad numbers before.
type Watch struct {
	Mean, Peak float64
	Samples    int
	Worst      []Proc
}

// Clean reports whether the run had the machine to itself, within max.
func (w Watch) Clean(max float64) bool { return w.Samples == 0 || w.Mean <= max }

// Summary is the one line a sweep prints about the conditions it measured in.
func (w Watch) Summary() string {
	if w.Samples == 0 {
		return "load not sampled"
	}
	s := fmt.Sprintf("%.1f cores of other work on average, %.1f at peak", w.Mean, w.Peak)
	if len(w.Worst) > 0 {
		s += ", worst: " + w.Worst[0].String()
	}
	return s
}

// WatchLoad samples the load foreign to pid's process tree every interval until
// done closes. It samples once at the start too, so a command that finishes
// inside a single interval is still judged on evidence rather than reported as
// quiet by default.
func WatchLoad(pid int, interval time.Duration, done <-chan struct{}) <-chan Watch {
	result := make(chan Watch, 1)
	go func() {
		var w Watch
		var total float64
		sample := func() {
			busy, top, err := ForeignLoad(pid)
			if err != nil {
				return
			}
			w.Samples++
			total += busy
			if busy > w.Peak {
				w.Peak, w.Worst = busy, worst(top)
			}
		}
		sample()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				if w.Samples > 0 {
					w.Mean = total / float64(w.Samples)
				}
				result <- w
				return
			case <-t.C:
				sample()
			}
		}
	}()
	return result
}
