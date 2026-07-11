package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// lockTimeout bounds how long an acquirer waits for the store lock; the
// operations it guards are millisecond-scale file writes, so hitting this
// means something is wedged, not busy.
var lockTimeout = 10 * time.Second

// Lock is an exclusive OS file lock on <home>/.lock, serializing ledger
// check-then-act sequences -- above all number allocation, where two readers
// of the same ledger otherwise both mint the same NextNum -- across
// PROCESSES: several agent CLIs and the server all write one store, and the
// server's in-process mutex cannot see a concurrent CLI. flock releases on
// process exit, so short-lived CLI invocations may simply hold it until they
// finish.
type Lock struct {
	f *os.File
}

// AcquireLock blocks until the store lock is held or the timeout passes.
func AcquireLock(home string) (*Lock, error) {
	f, err := os.OpenFile(filepath.Join(home, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{f: f}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("store is locked by another taskman process (waited %s)", lockTimeout)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// Release drops the lock; safe to skip in a short-lived process, since the
// OS releases flocks at exit.
func (l *Lock) Release() {
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
}

// procLocks tracks stores this process already holds, keyed by home: flock
// contends between file descriptions even within one process, so a second
// acquire on a fresh descriptor would self-deadlock. The CLI acquires once
// per store and holds to exit (run() is invoked repeatedly inside one test
// process, which is where this matters).
var (
	procMu    sync.Mutex
	procLocks = map[string]*Lock{}
)

// AcquireProcessLock takes the store lock once for this process and keeps
// it; later calls for the same store are no-ops. Exit releases it.
func AcquireProcessLock(home string) error {
	procMu.Lock()
	defer procMu.Unlock()
	if procLocks[home] != nil {
		return nil
	}
	l, err := AcquireLock(home)
	if err != nil {
		return err
	}
	procLocks[home] = l
	return nil
}
