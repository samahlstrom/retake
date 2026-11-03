// Command retake serves the review page for a video: press R, talk and draw over it, press R again.
// See spec/api.md and `retake help`.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/samahlstrom/retake/internal/cli"
	"github.com/samahlstrom/retake/internal/desktop"
)

func main() {
	exe := desktop.Exe()
	desktop.UseBundle(exe) // a release's own herdr, ffmpeg and retake-edit come first (spec/setup.md)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Main(ctx, desktop.AppArgs(exe, os.Args[1:]), cli.Env{})
	stop()
	os.Exit(code)
}
