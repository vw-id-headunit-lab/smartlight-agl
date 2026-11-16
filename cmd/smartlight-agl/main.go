package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/vw-id-headunit-lab/smartlight-agl/internal/smartlight"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := smartlight.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
