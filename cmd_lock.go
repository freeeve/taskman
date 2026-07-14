package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/freeeve/taskman/internal/lock"
	"github.com/freeeve/taskman/internal/store"
)

// cmdLock dispatches the resource-lock subcommands. Unlike every other
// command, these touch no ledger and take no store lock: a bench lock is held
// for the length of a sweep, and holding the store's flock for 45 minutes
// would wedge every other session's ledger writes.
func cmdLock(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taskman lock <acquire|release|heartbeat|status|steal|run> ...")
	}
	switch args[0] {
	case "acquire":
		return lockAcquire(args[1:])
	case "release":
		return lockRelease(args[1:])
	case "heartbeat", "refresh":
		return lockHeartbeat(args[1:])
	case "status":
		return lockStatus(args[1:])
	case "steal":
		return lockSteal(args[1:])
	case "run":
		return lockRun(args[1:])
	default:
		return fmt.Errorf("unknown lock subcommand %q (acquire, release, heartbeat, status, steal, run)", args[0])
	}
}

// lockHome resolves the store root and the project recorded as the holder.
// The project is advisory metadata (it tells a human which session to go
// shout at), so an unresolvable one is not fatal.
func lockHome(project string) (home, name string, err error) {
	home, err = store.Ensure()
	if err != nil {
		return "", "", err
	}
	name, _ = store.Resolve(project)
	return home, name, nil
}

// holder builds the metadata written into the lock file.
func holder(resource, project, reason string, ttl time.Duration) lock.Meta {
	host, _ := os.Hostname()
	return lock.Meta{
		Resource: resource,
		Project:  project,
		Host:     host,
		PID:      os.Getpid(),
		Session:  os.Getenv("TASKMAN_SESSION"),
		Reason:   reason,
		TTLSec:   int(ttl.Seconds()),
	}
}

// holderToken resolves the proof of ownership a release or heartbeat must
// present: the -token flag, else what acquire printed and the sweep script
// exported.
func holderToken(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv(lock.EnvToken)
}

// warnBroken says loudly that a dead session's lock was taken: a broken lock
// means a sweep was killed mid-run, and whatever it published may be junk.
func warnBroken(m *lock.Meta) {
	if m == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "taskman: broke the expired lock on %s held by %s since %s (no heartbeat for %s)\n",
		m.Resource, m.Holder(), m.StartedAt.Local().Format("15:04:05"),
		time.Since(m.Beat).Round(time.Second))
}

// noGate is the -max-load value meaning "do not gate on load at all"; 0 has to
// stay available to mean "require a completely idle machine".
const noGate = -1

// contaminatedExit is what a wrapped command exits with when it succeeded but
// did not have the machine to itself. It is distinct from the command's own
// failure codes so a sweep can tell "the run broke" from "the run is untrustworthy".
const contaminatedExit = 3

// loadSample is how often a wrapped command's machine is checked for foreign
// work. Sweeps run for minutes; a five-second cadence is plenty to catch a
// daemon eating a core and cheap enough to be invisible.
const loadSample = 5 * time.Second

// lockAcquire takes a resource lock, optionally waiting out a live holder.
// The token goes to stdout alone so a sweep script can capture it
// (TASKMAN_LOCK_TOKEN=$(taskman lock acquire local-cpu -wait 30m) || exit 1);
// everything a human wants to read goes to stderr.
func lockAcquire(args []string) error {
	fs := flag.NewFlagSet("lock acquire", flag.ContinueOnError)
	ttl := fs.Duration("ttl", lock.DefaultTTL, "how long the lock survives without a heartbeat")
	wait := fs.Duration("wait", 0, "how long to wait for a live holder to release")
	reason := fs.String("reason", "", "what the lock is being held for")
	project := fs.String("p", "", "project recorded as the holder (default: resolved from the current directory)")
	maxLoad := fs.Float64("max-load", noGate, "refuse to start unless other work is under this many cores (default: no gate)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman lock acquire [-ttl 45m] [-wait 30m] [-max-load 2] [-reason why] <resource>")
	}
	home, name, err := lockHome(*project)
	if err != nil {
		return err
	}
	held, broke, err := lock.Acquire(home, holder(fs.Arg(0), name, *reason, *ttl), *wait)
	warnBroken(broke)
	if err != nil {
		return err
	}
	if err := gate(home, held, *maxLoad, *wait); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "acquired %s for %s (ttl %s); release with: taskman lock release -token %s %s\n",
		held.Resource, held.Project, held.TTL(), held.Token, held.Resource)
	fmt.Println(held.Token)
	return nil
}

// gate holds the resource while the machine settles, and hands it back if it
// never does. Taking the lock first is what makes the wait worth anything: it
// stops further cooperating load, so what is left to wait out is the traffic no
// lock can reach -- a sibling's build finishing, a VM winding down.
//
// A run that cannot have the machine to itself must not start, and must not sit
// on a resource it is not using.
func gate(home string, held lock.Meta, maxLoad float64, wait time.Duration) error {
	if maxLoad < 0 {
		return nil
	}
	err := lock.WaitForQuiet(maxLoad, wait)
	if err == nil {
		return nil
	}
	if _, rerr := lock.Release(home, held.Resource, held.Token); rerr != nil {
		fmt.Fprintln(os.Stderr, "taskman:", rerr)
	}
	return err
}

// lockRelease drops a lock the caller holds.
func lockRelease(args []string) error {
	fs := flag.NewFlagSet("lock release", flag.ContinueOnError)
	token := fs.String("token", "", "the token acquire printed (default: $"+lock.EnvToken+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman lock release [-token t] <resource>")
	}
	home, err := store.Home()
	if err != nil {
		return err
	}
	m, err := lock.Release(home, fs.Arg(0), holderToken(*token))
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "released %s (held %s)\n", m.Resource, time.Since(m.StartedAt).Round(time.Second))
	return nil
}

// lockHeartbeat refreshes a lock the caller holds, so a sweep outliving its
// TTL is not broken mid-run.
func lockHeartbeat(args []string) error {
	fs := flag.NewFlagSet("lock heartbeat", flag.ContinueOnError)
	token := fs.String("token", "", "the token acquire printed (default: $"+lock.EnvToken+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman lock heartbeat [-token t] <resource>")
	}
	home, err := store.Home()
	if err != nil {
		return err
	}
	m, err := lock.Heartbeat(home, fs.Arg(0), holderToken(*token))
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s good for another %s\n", m.Resource, m.TTL())
	return nil
}

// lockSteal breaks a lock that is wedged but not yet expired -- the human
// override, which says plainly whose run it just orphaned.
func lockSteal(args []string) error {
	fs := flag.NewFlagSet("lock steal", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman lock steal <resource>")
	}
	home, err := store.Home()
	if err != nil {
		return err
	}
	m, err := lock.Steal(home, fs.Arg(0))
	if err != nil {
		return err
	}
	if m.Token == "" {
		fmt.Fprintf(os.Stderr, "cleared an unreadable lock file on %s\n", m.Resource)
		return nil
	}
	fmt.Fprintf(os.Stderr, "stole %s from %s (held %s, reason: %s)\n",
		m.Resource, m.Holder(), time.Since(m.StartedAt).Round(time.Second), reasonOr(m.Reason))
	fmt.Fprintf(os.Stderr, "taskman: that run keeps going -- it only learns it lost %s at its next heartbeat\n", m.Resource)
	return nil
}

// lockStatus prints the locks in the store: every one, or just the named
// resource.
func lockStatus(args []string) error {
	fs := flag.NewFlagSet("lock status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: taskman lock status [<resource>]")
	}
	home, err := store.Home()
	if err != nil {
		return err
	}
	locks, err := lock.List(home)
	if err != nil {
		return err
	}
	if fs.NArg() == 1 {
		var only []lock.Meta
		for _, m := range locks {
			if m.Resource == fs.Arg(0) {
				only = append(only, m)
			}
		}
		locks = only
	}
	if len(locks) == 0 {
		fmt.Println("no locks held")
		return nil
	}
	now := time.Now()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RESOURCE\tHOLDER\tPID\tHELD\tLEFT\tREASON")
	for _, m := range locks {
		left := "expired"
		if !m.Expired(now) {
			left = m.Expires().Sub(now).Round(time.Second).String()
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", m.Resource, hostProject(m), m.PID,
			now.Sub(m.StartedAt).Round(time.Second), left, reasonOr(m.Reason))
	}
	return w.Flush()
}

// hostProject labels a holder compactly for the status table.
func hostProject(m lock.Meta) string {
	if m.Project == "" {
		return m.Host
	}
	return m.Project + "@" + m.Host
}

// reasonOr keeps an empty reason from printing as a blank column.
func reasonOr(reason string) string {
	if reason == "" {
		return "-"
	}
	return reason
}

// lockRun holds a resource for the lifetime of a command: it acquires, runs
// the command, heartbeats while it runs, releases when it exits, and exits
// with the command's status. This is the shape a sweep should use -- the
// heartbeat is what lets a long run keep a short TTL, so a kill -9 frees the
// resource in minutes rather than hours.
func lockRun(args []string) error {
	fs := flag.NewFlagSet("lock run", flag.ContinueOnError)
	ttl := fs.Duration("ttl", lock.DefaultTTL, "how long the lock survives without a heartbeat")
	wait := fs.Duration("wait", 0, "how long to wait for a live holder to release")
	reason := fs.String("reason", "", "what the lock is being held for")
	project := fs.String("p", "", "project recorded as the holder (default: resolved from the current directory)")
	maxLoad := fs.Float64("max-load", noGate, "require other work to stay under this many cores, before and during the command (default: no gate)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) > 1 && rest[1] == "--" {
		rest = append(rest[:1], rest[2:]...)
	}
	if len(rest) < 2 {
		return fmt.Errorf("usage: taskman lock run [-ttl 45m] [-wait 30m] [-max-load 2] [-reason why] <resource> [--] <command> [args...]")
	}
	home, name, err := lockHome(*project)
	if err != nil {
		return err
	}
	held, broke, err := lock.Acquire(home, holder(rest[0], name, *reason, *ttl), *wait)
	warnBroken(broke)
	if err != nil {
		return err
	}
	if err := gate(home, held, *maxLoad, *wait); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "acquired %s for %s (ttl %s)\n", held.Resource, held.Project, held.TTL())

	cmd := exec.Command(rest[1], rest[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		if _, rerr := lock.Release(home, held.Resource, held.Token); rerr != nil {
			fmt.Fprintln(os.Stderr, "taskman:", rerr)
		}
		return err
	}

	// Forward an interrupt to the command rather than dying under it: taskman
	// has to outlive the child by one step to release the lock.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	done := make(chan struct{})
	go beat(home, held, done)
	go func() {
		for {
			select {
			case s := <-sigs:
				_ = cmd.Process.Signal(s)
			case <-done:
				return
			}
		}
	}()
	// Watch everything outside taskman's own process tree: the command being
	// timed is a child of ours, so its threads are excluded and its own CPU is
	// never mistaken for contamination.
	watching := lock.WatchLoad(os.Getpid(), loadSample, done)

	runErr := cmd.Wait()
	close(done)
	watch := <-watching
	if _, err := lock.Release(home, held.Resource, held.Token); err != nil {
		fmt.Fprintln(os.Stderr, "taskman:", err)
	} else {
		fmt.Fprintf(os.Stderr, "released %s (held %s)\n", held.Resource, time.Since(held.StartedAt).Round(time.Second))
	}
	dirty := *maxLoad >= 0 && !watch.Clean(*maxLoad)
	if *maxLoad >= 0 {
		if dirty {
			fmt.Fprintf(os.Stderr, "taskman: %s did NOT have the machine to itself: %s, over the %.1f cores allowed\n",
				held.Resource, watch.Summary(), *maxLoad)
			fmt.Fprintln(os.Stderr, "taskman: these timings are not trustworthy -- do not publish them")
		} else {
			fmt.Fprintf(os.Stderr, "%s ran on a quiet machine (%s)\n", held.Resource, watch.Summary())
		}
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		os.Exit(exit.ExitCode())
	}
	if runErr != nil {
		return runErr
	}
	if dirty {
		// The command succeeded, so its own status cannot carry this: exit
		// non-zero anyway, on a code of our own, so `... || exit 1` in a sweep
		// refuses to publish a run the machine spoiled.
		os.Exit(contaminatedExit)
	}
	return nil
}

// beat refreshes the lock until done closes. It heartbeats at a third of the
// TTL so a single missed beat (a hiccup, a busy disk) does not lose the
// resource. Losing the lock mid-run is loud: whatever the command is
// measuring now shares the machine with someone else.
func beat(home string, held lock.Meta, done <-chan struct{}) {
	t := time.NewTicker(max(held.TTL()/3, time.Second))
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if _, err := lock.Heartbeat(home, held.Resource, held.Token); err != nil {
				fmt.Fprintf(os.Stderr, "taskman: lost the lock on %s (%v); this run no longer has the resource to itself\n",
					held.Resource, err)
				return
			}
		}
	}
}
