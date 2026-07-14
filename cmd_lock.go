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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: taskman lock acquire [-ttl 45m] [-wait 30m] [-reason why] <resource>")
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
	fmt.Fprintf(os.Stderr, "acquired %s for %s (ttl %s); release with: taskman lock release -token %s %s\n",
		held.Resource, held.Project, held.TTL(), held.Token, held.Resource)
	fmt.Println(held.Token)
	return nil
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) > 1 && rest[1] == "--" {
		rest = append(rest[:1], rest[2:]...)
	}
	if len(rest) < 2 {
		return fmt.Errorf("usage: taskman lock run [-ttl 45m] [-wait 30m] [-reason why] <resource> [--] <command> [args...]")
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

	runErr := cmd.Wait()
	close(done)
	if _, err := lock.Release(home, held.Resource, held.Token); err != nil {
		fmt.Fprintln(os.Stderr, "taskman:", err)
	} else {
		fmt.Fprintf(os.Stderr, "released %s (held %s)\n", held.Resource, time.Since(held.StartedAt).Round(time.Second))
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		os.Exit(exit.ExitCode())
	}
	return runErr
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
