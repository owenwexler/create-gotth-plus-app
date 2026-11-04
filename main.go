// Command create-gotth-plus-app scaffolds a new GOTTH+ web app.
//
//	go run github.com/owenwexler/create-gotth-plus-app@latest
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/owenwexler/create-gotth-plus-app/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], cli.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Getwd: os.Getwd})
	stop()
	os.Exit(code)
}
