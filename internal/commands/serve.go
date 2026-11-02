package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/cli"
	"github.com/dinhhuy258/bx/internal/serve"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/tmux"
)

func init() {
	cli.Register(cli.Command{Name: "serve", Help: "serve [--listen host:port]: the daemon: projects, stories, sessions and their streams (Tailscale only)", Run: runServe})
}

func runServe(args []string, out, errOut io.Writer) int {
	cfg, a, logger, err := setup()
	if err != nil {
		fmt.Fprintln(errOut, "bx serve:", err)
		return 1
	}
	listen := cfg.Serve.Listen
	for i := 0; i < len(args); i++ {
		if args[i] == "--listen" && i+1 < len(args) {
			i++
			listen = args[i]
		}
	}
	addr, err := serve.ResolveListen(listen)
	if err != nil {
		fmt.Fprintln(errOut, "bx serve:", err)
		return 1
	}
	lock, err := state.Lock(filepath.Join(cfg.Root, "run", "serve"))
	if err != nil {
		fmt.Fprintln(errOut, "bx serve:", err)
		return 1
	}
	defer lock.Unlock()
	if err := tmux.WriteConf(cfg.TmuxConf()); err != nil {
		fmt.Fprintln(errOut, "bx serve:", err)
		return 1
	}
	log := func(format string, v ...any) {
		logger.Printf(format, v...)
		fmt.Fprintf(out, format+"\n", v...)
	}
	s := serve.New(a, cfg, claude.CLI{}, log)
	s.ResumeAll(context.Background()) // after a reboot: start every session that should be running
	s.KeepTmuxServer()                // a tmux server already running outlives its last session
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go s.Watch(ctx, serve.WatchEvery) // a session whose Claude exits becomes done
	go func() {
		<-ctx.Done()
		log("bx serve: shutting down (Claude sessions keep running in tmux)")
		s.Shutdown()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log("bx serve: listening on http://%s (Tailscale only, no login) · tmux -S %s", addr, cfg.TmuxSocket())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(errOut, "bx serve:", err)
		return 1
	}
	return 0
}
