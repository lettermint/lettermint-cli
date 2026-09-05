package main

import (
	"context"
	"github.com/lettermint/lettermint-cli/internal/command"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"
var clientID = ""

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := command.Execute(ctx, os.Args[1:], version, clientID, os.Stdin, os.Stdout, os.Stderr)
	stop()
	if code != 0 {
		os.Exit(code)
	}
}
