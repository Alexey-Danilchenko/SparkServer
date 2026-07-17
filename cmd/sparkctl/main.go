// sparkctl provides a small operator CLI for inspecting a local Spark Server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"sparkserver/internal/monitorcli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := monitorcli.Run(ctx, os.Args[1:], monitorcli.Options{}); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "sparkctl:", err)
		os.Exit(1)
	}
}
