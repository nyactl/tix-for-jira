// Command tix-jira works on your own Jira Cloud tickets from the terminal
// or as an MCP server, with a privacy scope and confirmed writes.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/nyactl/tix-jira/internal/cli"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli.Default(version).Execute(ctx, os.Args[1:]); err != nil {
		os.Exit(1)
	}
}
