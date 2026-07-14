// Package lock provides machine-scoped mutual exclusion over named resources
// (a CPU, a remote instance) for cooperating processes that share the taskman
// store but nothing else -- benchmark sweeps in sibling repos, run by sessions
// that cannot see each other, whose timings are silently inflated when two
// overlap.
//
// A lock is a single file under <home>/.locks/, created with link(2): the
// kernel fails the second creator with EEXIST, which is the whole primitive.
// Locks are machine state, not ledger history: the directory is gitignored and
// nothing here commits. Task status cannot stand in for this -- the store is a
// multi-writer git ledger with no cross-process locking, so a status-flag claim
// races exactly the way number allocation does.
//
// Every lock carries a TTL and a heartbeat, so a holder that is killed
// mid-sweep does not wedge the resource forever, and a token, so a holder whose
// lock was broken cannot later release its successor's.
package lock

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DefaultTTL bounds how long a lock survives without a heartbeat. It is a
// backstop for a dead holder, not a budget for the work: a sweep that outlives
// it should heartbeat (see Heartbeat) rather than raise it.
const DefaultTTL = 30 * time.Minute

// EnvToken is where a release or heartbeat looks for the holder's token when
// no flag carries it: the sweep script exports what acquire printed.
const EnvToken = "TASKMAN_LOCK_TOKEN"

// pollInterval is how often a waiting acquirer retries. Bench locks are held
// for minutes, so a coarse poll costs nothing and keeps the store quiet.
const pollInterval = 500 * time.Millisecond

// resourceRe constrains resource names to a flat, path-safe token: the name
// becomes a filename, so a separator or a leading dot must never reach it.
var resourceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Meta is a held lock: who holds it, since when, and for how long without a
// heartbeat. It is the entire content of the lock file.
type Meta struct {
	Resource  string    `json:"resource"`
	Project   string    `json:"project"`
	Host      string    `json:"host"`
	PID       int       `json:"pid"`
	Session   string    `json:"session,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Token     string    `json:"token"`
	StartedAt time.Time `json:"started_at"`
	Beat      time.Time `json:"heartbeat"`
	TTLSec    int       `json:"ttl_seconds"`
}

// TTL is how long the lock survives without a heartbeat.
func (m Meta) TTL() time.Duration { return time.Duration(m.TTLSec) * time.Second }

// Expires is when the lock becomes breakable by the next acquirer.
func (m Meta) Expires() time.Time { return m.Beat.Add(m.TTL()) }

// Expired reports whether the holder has missed its TTL, which means it died
// or hung: a live holder heartbeats.
func (m Meta) Expired(now time.Time) bool { return now.After(m.Expires()) }

// Holder describes the holder for a human: "rustychickpeas-ldbc on boxname
// (pid 4711)".
func (m Meta) Holder() string {
	s := fmt.Sprintf("%s on %s (pid %d)", m.Project, m.Host, m.PID)
	if m.Project == "" {
		s = fmt.Sprintf("%s (pid %d)", m.Host, m.PID)
	}
	return s
}

// BusyError reports that a live holder kept the lock for the whole wait. It is
// what makes `taskman lock acquire ... || exit 1` a usable gate in a sweep
// script.
type BusyError struct {
	Held   Meta
	Waited time.Duration
}

func (e *BusyError) Error() string {
	left := time.Until(e.Held.Expires()).Round(time.Second)
	s := fmt.Sprintf("%s is held by %s", e.Held.Resource, e.Held.Holder())
	if e.Held.Reason != "" {
		s += fmt.Sprintf(", reason: %s", e.Held.Reason)
	}
	s += fmt.Sprintf(" (expires in %s)", left)
	if e.Waited > 0 {
		s += fmt.Sprintf("; waited %s", e.Waited)
	}
	return s
}

// NotHeldError reports an operation on a resource nobody holds.
type NotHeldError struct{ Resource string }

func (e *NotHeldError) Error() string { return fmt.Sprintf("%s is not held", e.Resource) }

// NotHolderError reports a release or heartbeat presenting the wrong token --
// the holder's lock expired and was broken, or a human stole it, and the
// resource now belongs to someone else. Blindly releasing here is the classic
// "B releases A's stale lock, then A finishes" bug.
type NotHolderError struct{ Held Meta }

func (e *NotHolderError) Error() string {
	return fmt.Sprintf("%s is held by %s, not by you: your lock was broken after its TTL or stolen",
		e.Held.Resource, e.Held.Holder())
}

// Dir is the store's lock directory. It holds machine state, so it is
// gitignored and never committed.
func Dir(home string) string { return filepath.Join(home, ".locks") }

// Path is the lock file for a resource.
func Path(home, resource string) string {
	return filepath.Join(Dir(home), resource+".json")
}

// CheckResource rejects names that cannot be a lock filename. Resources are
// free-form on purpose -- local-cpu, ragedb-ec2 and neptune-aws contend for
// different hardware and must not serialize against each other -- but they are
// flat tokens, not paths.
func CheckResource(resource string) error {
	if !resourceRe.MatchString(resource) {
		return fmt.Errorf("resource %q must be 1-64 chars of letters, digits, dot, dash or underscore, starting with a letter or digit", resource)
	}
	return nil
}

// Acquire takes the lock, waiting up to wait for a live holder to release it,
// and returns the lock held plus the expired holder it broke, if any -- a
// broken lock means a session died mid-run, which the caller should say loudly.
// It fails with *BusyError when a live holder outlasts the wait.
func Acquire(home string, m Meta, wait time.Duration) (Meta, *Meta, error) {
	if err := CheckResource(m.Resource); err != nil {
		return Meta{}, nil, err
	}
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		return Meta{}, nil, err
	}
	if m.TTLSec <= 0 {
		m.TTLSec = int(DefaultTTL.Seconds())
	}
	var broke *Meta
	deadline := time.Now().Add(wait)
	for {
		token, err := newToken()
		if err != nil {
			return Meta{}, broke, err
		}
		m.Token = token
		m.StartedAt = time.Now().UTC()
		m.Beat = m.StartedAt
		err = create(home, m)
		if err == nil {
			return m, broke, nil
		}
		if !os.IsExist(err) {
			return Meta{}, broke, err
		}
		held, ok, err := Read(home, m.Resource)
		if err != nil {
			return Meta{}, broke, err
		}
		if !ok {
			continue // released in the window; try again straight away
		}
		if held.Expired(time.Now()) {
			// The holder missed its TTL: break its lock and take the
			// resource. A racing acquirer may break it first, in which case
			// takeAway fails and the next round contends with the winner.
			if dead, err := takeAway(home, held.Resource, held.Token); err == nil {
				broke = &dead
			}
			continue
		}
		if time.Now().After(deadline) {
			return Meta{}, broke, &BusyError{Held: held, Waited: wait}
		}
		time.Sleep(pollInterval)
	}
}

// Release drops the lock, refusing unless the caller presents the token it
// acquired with.
func Release(home, resource, token string) (Meta, error) {
	if err := CheckResource(resource); err != nil {
		return Meta{}, err
	}
	if token == "" {
		return Meta{}, fmt.Errorf("no token: pass -token, or export the one acquire printed as $%s", EnvToken)
	}
	return takeAway(home, resource, token)
}

// Steal drops the lock whoever holds it: the human override for a holder that
// is wedged but not yet expired. It returns the holder it displaced. The old
// holder keeps running (a cooperative lock cannot stop it) but its next
// heartbeat or release now fails, which is how it learns it lost the resource.
func Steal(home, resource string) (Meta, error) {
	if err := CheckResource(resource); err != nil {
		return Meta{}, err
	}
	return takeAway(home, resource, "")
}

// Heartbeat refreshes the holder's TTL, so a sweep outliving its TTL keeps the
// resource instead of being broken mid-run. It fails with *NotHolderError once
// the lock has been broken or stolen -- the holder's cue that its timings are
// no longer trustworthy.
func Heartbeat(home, resource, token string) (Meta, error) {
	if err := CheckResource(resource); err != nil {
		return Meta{}, err
	}
	if token == "" {
		return Meta{}, fmt.Errorf("no token: pass -token, or export the one acquire printed as $%s", EnvToken)
	}
	m, ok, err := Read(home, resource)
	if err != nil {
		return Meta{}, err
	}
	if !ok {
		return Meta{}, &NotHeldError{Resource: resource}
	}
	if m.Token != token {
		return Meta{}, &NotHolderError{Held: m}
	}
	m.Beat = time.Now().UTC()
	return m, replace(home, m)
}

// Read returns the lock held on a resource, if any.
func Read(home, resource string) (Meta, bool, error) {
	m, err := readFile(Path(home, resource))
	if os.IsNotExist(err) {
		return Meta{}, false, nil
	}
	if err != nil {
		return Meta{}, false, err
	}
	return m, true, nil
}

// List returns every lock in the store, held or expired, sorted by resource.
func List(home string) ([]Meta, error) {
	entries, err := os.ReadDir(Dir(home))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var locks []Meta
	for _, e := range entries {
		name := e.Name()
		// Temp files (link source) and files renamed aside by a break are
		// transient; only <resource>.json is a lock.
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		m, err := readFile(filepath.Join(Dir(home), name))
		if err != nil {
			if os.IsNotExist(err) {
				continue // released while we listed
			}
			return nil, err
		}
		locks = append(locks, m)
	}
	sort.Slice(locks, func(i, j int) bool { return locks[i].Resource < locks[j].Resource })
	return locks, nil
}

// create writes the lock file with link(2), which fails if the resource is
// already held. The content is written to a temp file first, so the lock file
// never exists half-written and a reader never sees a partial holder.
func create(home string, m Meta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := writeTemp(home, append(data, '\n'))
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Link(tmp, Path(home, m.Resource))
}

// replace overwrites an existing lock file atomically (a heartbeat's new
// timestamp); unlike create it does not guard against a concurrent holder,
// which is why only the token holder may call it.
func replace(home string, m Meta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := writeTemp(home, append(data, '\n'))
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, Path(home, m.Resource)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// writeTemp writes data to a fresh file in the lock directory and returns its
// path.
func writeTemp(home string, data []byte) (string, error) {
	f, err := os.CreateTemp(Dir(home), ".tmp-*")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// takeAway removes the lock file if it still carries wantToken (any token when
// wantToken is empty, which is what a steal does) and returns the holder it
// removed.
//
// The file is renamed aside before the token is checked, not after: only one
// racer's rename can find the file, so two acquirers breaking the same expired
// lock cannot both believe they broke it. If the token turns out not to match
// -- someone re-acquired in the window -- the file is linked back, which fails
// harmlessly if the winner has already created a new lock.
func takeAway(home, resource, wantToken string) (Meta, error) {
	p := Path(home, resource)
	suffix, err := newToken()
	if err != nil {
		return Meta{}, err
	}
	aside := p + ".taken-" + suffix
	if err := os.Rename(p, aside); err != nil {
		if os.IsNotExist(err) {
			return Meta{}, &NotHeldError{Resource: resource}
		}
		return Meta{}, err
	}
	m, err := readFile(aside)
	if err != nil {
		os.Remove(aside)
		if wantToken == "" {
			// A steal clears an unreadable lock instead of reporting it: a
			// lock nobody can parse is precisely the wedge steal exists to
			// break, and it is now gone.
			return Meta{Resource: resource}, nil
		}
		return Meta{}, err
	}
	if wantToken != "" && m.Token != wantToken {
		if err := os.Link(aside, p); err != nil && !os.IsExist(err) {
			os.Remove(aside)
			return Meta{}, err
		}
		os.Remove(aside)
		return Meta{}, &NotHolderError{Held: m}
	}
	return m, os.Remove(aside)
}

// readFile parses one lock file.
func readFile(path string) (Meta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Meta{}, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return Meta{}, fmt.Errorf("%s is not a readable lock (%v); clear it with taskman lock steal", filepath.Base(path), err)
	}
	return m, nil
}

// newToken mints the holder's proof of ownership.
func newToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
