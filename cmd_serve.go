package main

import (
	"flag"
	"fmt"

	"github.com/freeeve/taskman/internal/store"
	"github.com/freeeve/taskman/internal/web"
)

// cmdServe runs the kanban web app over the central store.
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7777", "listen address")
	insecure := fs.Bool("insecure-bind", false, "allow binding a non-loopback address (the API has no auth)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: taskman serve [-addr host:port] [-insecure-bind]")
	}
	home, err := store.Ensure()
	if err != nil {
		return err
	}
	return web.Serve(*addr, home, *insecure)
}
