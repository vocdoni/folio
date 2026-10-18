// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/vocdoni/folio/internal/app"
	"github.com/vocdoni/folio/internal/config"
)

// serveCmd runs the MCP service until SIGTERM/SIGINT, then drains and exits.
func serveCmd(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "usage: folio serve (configured through FOLIO_* environment variables)")
		return 2
	}
	cfg, err := config.Load(os.LookupEnv, config.ModeServe)
	if err != nil {
		fmt.Fprintln(os.Stderr, "folio: invalid configuration:\n", err)
		return 2
	}
	log := app.NewLogger(cfg, os.Stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.New(ctx, cfg, app.Options{Version: version(), Logger: log})
	if err != nil {
		log.Error("startup failed", "err", err)
		return 1
	}
	defer a.Close() //nolint:errcheck // process exit follows
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		log.Error("listen failed", "addr", cfg.Addr, "err", err)
		return 1
	}
	if err := a.Run(ctx, ln); err != nil {
		log.Error("server stopped with an error", "err", err)
		return 1
	}
	log.Info("shut down cleanly")
	return 0
}
