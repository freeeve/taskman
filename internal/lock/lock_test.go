package lock

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// meta builds a holder for tests; ttl is in seconds so a test can craft an
// expiry without sleeping.
func meta(resource, project string, ttl int) Meta {
	return Meta{Resource: resource, Project: project, Host: "testbox", PID: 4711, TTLSec: ttl}
}

// acquire takes the lock or fails the test.
func acquire(t *testing.T, home string, m Meta) Meta {
	t.Helper()
	held, broke, err := Acquire(home, m, 0)
	if err != nil {
		t.Fatalf("acquire %s: %v", m.Resource, err)
	}
	if broke != nil {
		t.Fatalf("acquire %s broke a lock it should not have seen: %+v", m.Resource, *broke)
	}
	return held
}

// stale rewrites a held lock's heartbeat into the past, standing in for a
// holder that was SIGKILLed: the file survives, nothing refreshes it.
func stale(t *testing.T, home string, m Meta, age time.Duration) {
	t.Helper()
	m.Beat = time.Now().UTC().Add(-age)
	if err := replace(home, m); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireIsExclusive(t *testing.T) {
	home := t.TempDir()
	held := acquire(t, home, meta("local-cpu", "rustychickpeas", 300))

	_, _, err := Acquire(home, meta("local-cpu", "gochickpeas", 300), 0)
	var busy *BusyError
	if !errors.As(err, &busy) {
		t.Fatalf("second acquire = %v, want a BusyError", err)
	}
	if busy.Held.Project != "rustychickpeas" {
		t.Errorf("busy names %q as the holder, want rustychickpeas", busy.Held.Project)
	}
	if !strings.Contains(busy.Error(), "pid 4711") {
		t.Errorf("busy error %q does not name the holding process", busy.Error())
	}

	if _, err := Release(home, "local-cpu", held.Token); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, _, err := Acquire(home, meta("local-cpu", "gochickpeas", 300), 0); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

// A ragedb sweep is a thin client blocked on the network, so it must not queue
// behind a local rust sweep: only same-resource acquires block.
func TestDisjointResourcesDoNotBlock(t *testing.T) {
	home := t.TempDir()
	acquire(t, home, meta("local-cpu", "rustychickpeas-ldbc", 300))
	acquire(t, home, meta("ragedb-ec2", "rustychickpeas-ldbc", 300))
	acquire(t, home, meta("neptune-aws", "gochickpeas", 300))

	locks, err := List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 3 {
		t.Fatalf("List = %d locks, want 3", len(locks))
	}
	if locks[0].Resource != "local-cpu" || locks[2].Resource != "ragedb-ec2" {
		t.Errorf("List is not sorted by resource: %v", locks)
	}
}

// Killing the holder leaves a lock that a later acquirer can break after the
// TTL, not before.
func TestExpiredLockIsBreakableTTLNotBefore(t *testing.T) {
	home := t.TempDir()
	dead := acquire(t, home, meta("local-cpu", "rustychickpeas-ldbc", 60))

	stale(t, home, dead, 30*time.Second) // killed 30s ago, TTL 60s: still theirs
	if _, _, err := Acquire(home, meta("local-cpu", "gochickpeas", 60), 0); !errors.As(err, new(*BusyError)) {
		t.Fatalf("acquire inside the TTL = %v, want BusyError: a lock is not breakable before it expires", err)
	}

	stale(t, home, dead, 90*time.Second) // TTL passed with no heartbeat
	held, broke, err := Acquire(home, meta("local-cpu", "gochickpeas", 60), 0)
	if err != nil {
		t.Fatalf("acquire after the TTL: %v", err)
	}
	if broke == nil {
		t.Fatal("acquire took an expired lock silently; the dead holder must be reported")
	}
	if broke.Project != "rustychickpeas-ldbc" || broke.Token != dead.Token {
		t.Errorf("broke = %+v, want the dead holder's lock", *broke)
	}
	if held.Project != "gochickpeas" {
		t.Errorf("holder after the break = %q, want gochickpeas", held.Project)
	}
}

// The classic stale-lock bug: A's lock expires, B breaks it and acquires, then
// A finishes and releases -- which must not drop B's lock.
func TestReleaseAfterBreakDoesNotDropTheNewHolder(t *testing.T) {
	home := t.TempDir()
	a := acquire(t, home, meta("local-cpu", "rustychickpeas", 60))
	stale(t, home, a, 90*time.Second)
	b, broke, err := Acquire(home, meta("local-cpu", "gochickpeas", 60), 0)
	if err != nil || broke == nil {
		t.Fatalf("break-and-acquire: %v (broke %v)", err, broke)
	}

	_, err = Release(home, "local-cpu", a.Token)
	var wrong *NotHolderError
	if !errors.As(err, &wrong) {
		t.Fatalf("the broken holder's release = %v, want NotHolderError", err)
	}
	if wrong.Held.Token != b.Token {
		t.Errorf("NotHolderError names token %q, want the new holder's", wrong.Held.Token)
	}
	held, ok, err := Read(home, "local-cpu")
	if err != nil || !ok {
		t.Fatalf("read after the refused release: %v (held %v)", err, ok)
	}
	if held.Token != b.Token {
		t.Fatal("the broken holder's release dropped the new holder's lock")
	}
}

func TestReleaseRequiresTheToken(t *testing.T) {
	home := t.TempDir()
	acquire(t, home, meta("local-cpu", "rustychickpeas", 300))

	if _, err := Release(home, "local-cpu", ""); err == nil {
		t.Error("release with no token succeeded")
	}
	if _, err := Release(home, "local-cpu", "deadbeefdeadbeef"); !errors.As(err, new(*NotHolderError)) {
		t.Errorf("release with a wrong token = %v, want NotHolderError", err)
	}
	if _, ok, _ := Read(home, "local-cpu"); !ok {
		t.Error("a refused release dropped the lock anyway")
	}
	if _, err := Release(home, "ragedb-ec2", "deadbeefdeadbeef"); !errors.As(err, new(*NotHeldError)) {
		t.Errorf("release of an unheld resource = %v, want NotHeldError", err)
	}
}

func TestHeartbeatExtendsAndProvesOwnership(t *testing.T) {
	home := t.TempDir()
	held := acquire(t, home, meta("local-cpu", "rustychickpeas-ldbc", 60))
	stale(t, home, held, 50*time.Second) // 10s left

	beaten, err := Heartbeat(home, "local-cpu", held.Token)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if left := time.Until(beaten.Expires()); left < 55*time.Second {
		t.Errorf("heartbeat left %s on the lock, want the full 60s TTL back", left)
	}
	if _, _, err := Acquire(home, meta("local-cpu", "gochickpeas", 60), 0); !errors.As(err, new(*BusyError)) {
		t.Errorf("acquire after a heartbeat = %v, want BusyError: the holder is alive", err)
	}
	if _, err := Heartbeat(home, "local-cpu", "deadbeefdeadbeef"); !errors.As(err, new(*NotHolderError)) {
		t.Error("a non-holder refreshed the lock")
	}
}

// Steal is the human override: it drops a live holder's lock, and the holder
// finds out at its next heartbeat.
func TestStealDropsALiveLock(t *testing.T) {
	home := t.TempDir()
	held := acquire(t, home, meta("local-cpu", "rustychickpeas", 3600))

	lost, err := Steal(home, "local-cpu")
	if err != nil {
		t.Fatalf("steal: %v", err)
	}
	if lost.Project != "rustychickpeas" {
		t.Errorf("steal reports %q as the displaced holder, want rustychickpeas", lost.Project)
	}
	if _, err := Heartbeat(home, "local-cpu", held.Token); !errors.As(err, new(*NotHeldError)) {
		t.Errorf("the robbed holder's heartbeat = %v, want NotHeldError", err)
	}
	acquire(t, home, meta("local-cpu", "gochickpeas", 3600))
}

// Two concurrent acquires: exactly one wins. link(2) is the referee, so this
// holds across processes too -- the goroutines here just make the race easy to
// run.
func TestConcurrentAcquireHasOneWinner(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var won []Meta
	start := make(chan struct{})
	for range racers {
		wg.Go(func() {
			<-start
			held, _, err := Acquire(home, meta("local-cpu", "racer", 300), 0)
			if err != nil {
				return
			}
			mu.Lock()
			won = append(won, held)
			mu.Unlock()
		})
	}
	close(start)
	wg.Wait()
	if len(won) != 1 {
		t.Fatalf("%d of %d racers acquired local-cpu, want exactly 1", len(won), racers)
	}
	held, ok, err := Read(home, "local-cpu")
	if err != nil || !ok {
		t.Fatalf("read: %v (held %v)", err, ok)
	}
	if held.Token != won[0].Token {
		t.Error("the lock on disk is not the one the winner acquired")
	}
}

// A waiting acquirer gives up when a live holder outlasts the wait, so
// `taskman lock acquire -wait ... || exit 1` gates a sweep.
func TestAcquireWaitsThenFails(t *testing.T) {
	home := t.TempDir()
	acquire(t, home, meta("local-cpu", "rustychickpeas", 300))
	started := time.Now()
	if _, _, err := Acquire(home, meta("local-cpu", "gochickpeas", 300), 750*time.Millisecond); !errors.As(err, new(*BusyError)) {
		t.Fatalf("acquire = %v, want BusyError", err)
	}
	if waited := time.Since(started); waited < 750*time.Millisecond {
		t.Errorf("acquire gave up after %s, before the 750ms wait was out", waited)
	}
}

// A waiting acquirer takes the lock the moment the holder releases.
func TestAcquireWaitsForARelease(t *testing.T) {
	home := t.TempDir()
	held := acquire(t, home, meta("local-cpu", "rustychickpeas", 300))
	go func() {
		time.Sleep(200 * time.Millisecond)
		if _, err := Release(home, "local-cpu", held.Token); err != nil {
			panic(err)
		}
	}()
	if _, _, err := Acquire(home, meta("local-cpu", "gochickpeas", 300), 10*time.Second); err != nil {
		t.Fatalf("acquire waiting on a release: %v", err)
	}
}

func TestResourceNamesStayInsideTheLockDir(t *testing.T) {
	home := t.TempDir()
	for _, bad := range []string{"", ".", "..", "../escape", "a/b", "-leading", ".hidden", strings.Repeat("x", 65), "with space"} {
		if err := CheckResource(bad); err == nil {
			t.Errorf("CheckResource(%q) accepted a name that cannot be a lock file", bad)
		}
		if _, _, err := Acquire(home, meta(bad, "p", 60), 0); err == nil {
			t.Errorf("Acquire(%q) succeeded", bad)
		}
	}
	for _, ok := range []string{"local-cpu", "ragedb-ec2", "neptune-aws", "a", "a.b_c-d9"} {
		if err := CheckResource(ok); err != nil {
			t.Errorf("CheckResource(%q) = %v, want nil", ok, err)
		}
	}
}

// Nothing but <resource>.json is a lock: the temp files link(2) needs, and a
// file a break renamed aside, must never surface as a holder.
func TestListIgnoresNonLockFiles(t *testing.T) {
	home := t.TempDir()
	acquire(t, home, meta("local-cpu", "rustychickpeas", 300))
	for _, junk := range []string{".tmp-123", "local-cpu.json.taken-abc", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(Dir(home), junk), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	locks, err := List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 || locks[0].Resource != "local-cpu" {
		t.Fatalf("List = %v, want just the local-cpu lock", locks)
	}
}

func TestListOfAnEmptyStore(t *testing.T) {
	locks, err := List(t.TempDir())
	if err != nil {
		t.Fatalf("List of a store with no locks: %v", err)
	}
	if len(locks) != 0 {
		t.Fatalf("List = %v, want none", locks)
	}
}

// A lock file nobody can parse must not read as "free": it is reported, with
// the remedy, and steal is that remedy.
func TestCorruptLockFileIsReportedAndStealable(t *testing.T) {
	home := t.TempDir()
	acquire(t, home, meta("local-cpu", "rustychickpeas", 300))
	if err := os.WriteFile(Path(home, "local-cpu"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := Acquire(home, meta("local-cpu", "gochickpeas", 300), 0)
	if err == nil {
		t.Fatal("acquire over an unreadable lock succeeded; it may be a live holder")
	}
	if !strings.Contains(err.Error(), "steal") {
		t.Errorf("error %q does not point at the remedy", err)
	}
	if _, err := List(home); err == nil {
		t.Error("List reported an unreadable lock as fine")
	}

	if _, err := Steal(home, "local-cpu"); err != nil {
		t.Fatalf("steal of an unreadable lock: %v", err)
	}
	acquire(t, home, meta("local-cpu", "gochickpeas", 300))
}

// The errors a sweep script and a human read must name the resource, the
// holder, and what to do about it.
func TestErrorsNameTheResourceAndHolder(t *testing.T) {
	held := meta("local-cpu", "rustychickpeas-ldbc", 300)
	held.Reason = "sweep a8a13e9"
	held.Beat = time.Now()

	busy := (&BusyError{Held: held, Waited: 30 * time.Minute}).Error()
	for _, want := range []string{"local-cpu", "rustychickpeas-ldbc", "testbox", "pid 4711", "sweep a8a13e9", "waited 30m"} {
		if !strings.Contains(busy, want) {
			t.Errorf("BusyError %q does not mention %q", busy, want)
		}
	}
	if s := (&NotHeldError{Resource: "ragedb-ec2"}).Error(); !strings.Contains(s, "ragedb-ec2") {
		t.Errorf("NotHeldError = %q", s)
	}
	if s := (&NotHolderError{Held: held}).Error(); !strings.Contains(s, "rustychickpeas-ldbc") || !strings.Contains(s, "broken") {
		t.Errorf("NotHolderError %q must name the new holder and say the lock was broken", s)
	}
	// An unresolvable project still yields a usable holder label.
	anon := Meta{Host: "testbox", PID: 7}
	if s := anon.Holder(); !strings.Contains(s, "testbox") || !strings.Contains(s, "pid 7") {
		t.Errorf("Holder() with no project = %q", s)
	}
}

// FuzzCheckResource pins the property the filename layout rests on: an
// accepted resource is a flat token, so its lock file cannot land outside the
// lock directory.
func FuzzCheckResource(f *testing.F) {
	for _, seed := range []string{"local-cpu", "ragedb-ec2", "../../etc/passwd", "a/b", "", ".", "..", "x.json"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, resource string) {
		if err := CheckResource(resource); err != nil {
			return
		}
		dir := Dir("/store")
		p := Path("/store", resource)
		if filepath.Dir(p) != dir {
			t.Errorf("resource %q escapes the lock dir: %s", resource, p)
		}
		if p != filepath.Clean(p) {
			t.Errorf("resource %q yields an uncleaned path: %s", resource, p)
		}
		if strings.HasPrefix(filepath.Base(p), ".") {
			t.Errorf("resource %q yields a hidden file that List would skip: %s", resource, p)
		}
	})
}
