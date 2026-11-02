package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dinhhuy258/bx/internal/cli"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/notify"
	"github.com/dinhhuy258/bx/internal/serve"
	"github.com/dinhhuy258/bx/internal/state"
)

func init() {
	cli.Register(cli.Command{Name: "notify", Help: "internal: Claude Code hook → ntfy", Run: runNotify})
}

// runNotify is a Claude Code hook. It must exit 0 fast and never block Claude.
// The notification links to the session's page in the web client when a base URL is known
// (notify.open_url, else the serve.listen address).
func runNotify(args []string, out, errOut io.Writer) int {
	cfg, err := config.Load(config.DefaultRoot())
	if err != nil || cfg.Notify.NtfyURL == "" {
		return 0
	}
	h, err := notify.ParseHook(os.Stdin)
	if err != nil {
		return 0
	}
	project, num, title, ok := findSession(cfg, h.SessionID)
	if !ok {
		return 0 // a Claude started outside bx; not ours to announce
	}
	t, b := notify.Message(h, project, num, title)
	base, err := notify.ClickBase(cfg.Notify.OpenURL, cfg.Serve.Listen, serve.ResolveListen)
	if err != nil {
		fmt.Fprintln(errOut, "bx notify:", err)
	}
	click := ""
	if base != "" {
		click = notify.SessionURL(base, project, num)
	}
	if err := notify.Post(cfg.Notify.NtfyURL, t, b, click); err != nil {
		fmt.Fprintln(errOut, "bx notify:", err)
	}
	return 0
}

// findSession scans every state file for the session uuid.
func findSession(cfg *config.Config, uuid string) (project string, num int, title string, ok bool) {
	names, _ := state.ListProjects(filepath.Join(cfg.Root, "state"))
	for _, name := range names {
		p, err := state.Load(cfg.StateFile(name), name)
		if err != nil {
			continue
		}
		for n, s := range p.Sessions {
			if s.UUID == uuid {
				return name, n, s.Title, true
			}
		}
	}
	return "", 0, "", false
}
