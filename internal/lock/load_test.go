package lock

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestForeignLoadSeesTheMachine(t *testing.T) {
	busy, top, err := ForeignLoad(0)
	if err != nil {
		t.Fatalf("ForeignLoad: %v", err)
	}
	if busy < 0 {
		t.Errorf("busy = %v cores, want >= 0", busy)
	}
	for i, p := range top {
		if p.CPU <= 0 {
			t.Errorf("idle process in the load sample: %s", p)
		}
		if i > 0 && top[i-1].CPU < p.CPU {
			t.Errorf("processes are not sorted busiest first: %v", top[:i+1])
		}
	}
	if n := len(worst(top)); n > 3 {
		t.Errorf("worst() named %d processes, want at most 3", n)
	}
}

// The whole point of measuring FOREIGN load: a benchmark's own threads drive
// the machine hard by design, and must never be read as contamination. A busy
// child of ours is excluded; the same child is counted when it is not ours.
func TestForeignLoadExcludesOurOwnTree(t *testing.T) {
	// A shell loop with no forks in it: the process itself burns the core, which
	// is what ps must attribute to it. (A loop calling date(1) would fork the
	// work out to children and leave the shell looking idle.)
	spin := exec.Command("sh", "-c", "while :; do :; done")
	if err := spin.Start(); err != nil {
		t.Skipf("cannot spawn a busy child: %v", err)
	}
	defer func() {
		_ = spin.Process.Kill()
		_ = spin.Wait()
	}()
	time.Sleep(2 * time.Second) // ps's decayed average needs a moment to converge

	// Counting everything, the spinner is there, burning most of a core.
	_, all, err := ForeignLoad(0)
	if err != nil {
		t.Fatal(err)
	}
	spun := find(all, spin.Process.Pid)
	if spun == nil {
		t.Fatalf("a child burning a core is missing from the unfiltered process table")
	}
	if spun.CPU < 0.3 {
		t.Errorf("the spinner shows %.2f cores; ps is not seeing it burn", spun.CPU)
	}

	// Excluding our tree, it is gone: the command being timed is a child of
	// taskman, and its own CPU is the one thing that is never contamination.
	_, foreign, err := ForeignLoad(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if p := find(foreign, spin.Process.Pid); p != nil {
		t.Errorf("our own busy child was counted as foreign work: %s", p)
	}
	if p := find(foreign, os.Getpid()); p != nil {
		t.Errorf("taskman counted itself as foreign work: %s", p)
	}
}

// find returns the named process in a load sample, or nil.
func find(procs []Proc, pid int) *Proc {
	for _, p := range procs {
		if p.PID == pid {
			return &p
		}
	}
	return nil
}

func TestWaitForQuietPassesAndRefuses(t *testing.T) {
	// A ceiling no machine can exceed is always satisfied, at once.
	started := time.Now()
	if err := WaitForQuiet(1e6, time.Minute); err != nil {
		t.Fatalf("WaitForQuiet on an effectively idle ceiling: %v", err)
	}
	if waited := time.Since(started); waited > 5*time.Second {
		t.Errorf("a satisfied gate took %s; it must not poll", waited)
	}

	// A ceiling no machine can meet fails after the wait, naming the load.
	started = time.Now()
	err := WaitForQuiet(-1, 300*time.Millisecond)
	var loaded *LoadedError
	if !errors.As(err, &loaded) {
		t.Fatalf("WaitForQuiet on an unmeetable ceiling = %v, want LoadedError", err)
	}
	if time.Since(started) < 300*time.Millisecond {
		t.Error("the gate gave up before its wait was out")
	}
	if loaded.Waited == 0 {
		t.Error("LoadedError does not record how long it waited")
	}
	if !strings.Contains(loaded.Error(), "cores of other work") {
		t.Errorf("LoadedError = %q, want the load it refused on", loaded.Error())
	}
}

// A gate that will not say who broke it is a gate nobody can act on.
func TestLoadedErrorNamesTheOffenders(t *testing.T) {
	err := &LoadedError{
		Load: 11.2, Max: 2, Waited: 30 * time.Second,
		Top: []Proc{{PID: 87881, CPU: 0.76, Comm: "pebble_updater"}},
	}
	for _, want := range []string{"11.2", "2.0", "waited 30s", "pebble_updater", "pid 87881", "0.8 cores"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("LoadedError %q does not mention %q", err.Error(), want)
		}
	}
}

// The verdict rests on the mean, so a brief compile during a long sweep is
// noise while a daemon holding a core throughout is contamination.
func TestWatchVerdict(t *testing.T) {
	blip := Watch{Mean: 0.4, Peak: 6.0, Samples: 400}
	if !blip.Clean(2) {
		t.Error("a short spike in an otherwise quiet run must not condemn it")
	}
	sustained := Watch{Mean: 3.5, Peak: 6.0, Samples: 400,
		Worst: []Proc{{PID: 87881, CPU: 0.76, Comm: "pebble_updater"}}}
	if sustained.Clean(2) {
		t.Error("a run averaging 3.5 foreign cores against a ceiling of 2 is contaminated")
	}
	if s := sustained.Summary(); !strings.Contains(s, "3.5") || !strings.Contains(s, "pebble_updater") {
		t.Errorf("Summary = %q, want the mean and the worst offender", s)
	}
	// Nothing sampled (a command that exits immediately) is not a failure.
	if !(Watch{}).Clean(0) {
		t.Error("an unsampled run must not be reported as contaminated")
	}
	if s := (Watch{}).Summary(); !strings.Contains(s, "not sampled") {
		t.Errorf("Summary of an unsampled run = %q", s)
	}
}

func TestWatchLoadSamplesUntilDone(t *testing.T) {
	done := make(chan struct{})
	watching := WatchLoad(os.Getpid(), 50*time.Millisecond, done)
	time.Sleep(300 * time.Millisecond)
	close(done)
	select {
	case w := <-watching:
		if w.Samples == 0 {
			t.Fatal("WatchLoad took no samples in 300ms at a 50ms cadence")
		}
		if w.Mean < 0 || w.Peak < w.Mean {
			t.Errorf("Watch = %+v, want a peak at or above the mean", w)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WatchLoad did not report after done closed")
	}
}
