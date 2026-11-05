package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/claudeplugin"
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
	filesAddr, err := serve.ResolveFiles(cfg.Serve.FilesListen, addr)
	if err != nil {
		fmt.Fprintln(errOut, "bx serve: files_listen:", err)
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
	// before ResumeAll: every session it starts gets the plugin and its artifacts folder
	if err := claudeplugin.Write(cfg.PluginDir()); err != nil {
		fmt.Fprintln(errOut, "bx serve: claude plugin:", err)
		return 1
	}
	if err := os.MkdirAll(cfg.ArtifactsDir(), 0o700); err != nil {
		fmt.Fprintln(errOut, "bx serve:", err)
		return 1
	}
	a.PluginDir = cfg.PluginDir()
	a.APIURL = "http://" + addr
	if filesAddr != "" {
		a.ArtifactsURL = "http://" + filesAddr
	}
	log := func(format string, v ...any) {
		logger.Printf(format, v...)
		fmt.Fprintf(out, format+"\n", v...)
	}
	s := serve.New(a, cfg, claude.CLI{}, log)
	s.ResumeAll(context.Background()) // after a reboot: start every session that should be running
	s.KeepTmuxServer()                // a tmux server already running outlives its last session
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	var files *http.Server
	if filesAddr != "" {
		files = &http.Server{Addr: filesAddr, Handler: serve.FilesHandler(cfg.ArtifactsDir()), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := files.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log("bx serve: artifacts file server: %v", err)
			}
		}()
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go s.Watch(ctx, serve.WatchEvery) // a session whose Claude exits becomes done
	go s.WatchPRs(ctx)                // the PRs of story branches: badges, notifications
	go func() {
		<-ctx.Done()
		log("bx serve: shutting down (Claude sessions keep running in tmux)")
		s.Shutdown()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if files != nil {
			_ = files.Shutdown(shutdownCtx)
		}
		_ = srv.Shutdown(shutdownCtx)
	}()
	log("bx serve: listening on http://%s (Tailscale only, no login) · tmux -S %s", addr, cfg.TmuxSocket())
	if filesAddr != "" {
		log("bx serve: artifacts on http://%s (read-only, %s)", filesAddr, cfg.ArtifactsDir())
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(errOut, "bx serve:", err)
		return 1
	}
	return 0
}
