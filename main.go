// Command webtor is the CLI for the webtor.io API — see `webtor --help`.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/webtor-io/webtor-cli/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(cmd.Main(ctx, os.Args))
}
