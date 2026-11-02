package commands

import (
	"context"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"

	"github.com/dinhhuy258/bx/internal/cli"
	"github.com/dinhhuy258/bx/internal/client"
	"github.com/dinhhuy258/bx/internal/config"
)

func init() {
	cli.Register(cli.Command{Name: "client", Help: "[--server URL] [--project NAME]: the bx client (default)", Run: runClient})
}

func runClient(args []string, out, errOut io.Writer) int {
	cfg, err := config.Load(config.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, "bx:", err)
		return 1
	}
	server, project := cfg.Client.Server, ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--server" && i+1 < len(args):
			i++
			server = args[i]
		case args[i] == "--project" && i+1 < len(args):
			i++
			project = args[i]
		default:
			fmt.Fprintf(errOut, "bx: unknown argument %q\nusage: bx [--server URL] [--project NAME]\n", args[i])
			return 2
		}
	}
	if server == "" {
		fmt.Fprintln(errOut, "bx: no server. Pass --server http://100.x.y.z:7357 or set [client] server in", cfg.Root+"/config.toml")
		return 2
	}
	a := client.NewAPI(server)
	m := client.NewModel(cfg, a, project)
	prog := tea.NewProgram(m)
	m.SetSender(prog.Send)
	m.SetStreamer(func(ctx context.Context, project string, t client.Target, cols, rows int, term *client.Term, send func(tea.Msg)) (client.Conn, error) {
		url := a.StreamURL(project, t.Num, cols, rows)
		if t.Shell() {
			url = a.ShellStreamURL(project, t.Story, t.K, cols, rows)
		}
		return client.Connect(ctx, url, term, send)
	})
	if _, err := prog.Run(); err != nil {
		fmt.Fprintln(errOut, "bx:", err)
		return 1
	}
	return 0
}
