package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/claudesync"
	"github.com/dinhhuy258/bx/internal/cli"
	"github.com/dinhhuy258/bx/internal/client"
	"github.com/dinhhuy258/bx/internal/config"
)

const claudeHelp = "claude sync [folders] [--dry-run] [--yes]: copy Claude skills, agents, commands and rules to bx serve"

const syncUsage = "usage: bx claude sync [skills|agents|commands|rules ...] [--dry-run] [--yes] [--server URL]"

func init() {
	cli.Register(cli.Command{Name: "claude", Help: claudeHelp, Run: runClaude})
}

func runClaude(args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] != "sync" {
		fmt.Fprintln(errOut, syncUsage)
		return 2
	}
	return claudeSync(args[1:], syncIO{Out: out, Err: errOut, In: os.Stdin, TTY: isTerminal(os.Stdin)})
}

// syncIO is the terminal of bx claude sync; tests fill it in.
type syncIO struct {
	Out, Err io.Writer
	In       io.Reader
	TTY      bool // In is a terminal, so we may ask
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// claudeSync copies the Mac's Claude folders to bx serve: plan, show, ask, apply.
func claudeSync(args []string, sio syncIO) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(sio.Err, "bx claude sync: "+format+"\n", a...)
		return 1
	}
	cfg, err := config.Load(config.DefaultRoot())
	if err != nil {
		return fail("%v", err)
	}
	server, dryRun, yes := cfg.Client.Server, false, false
	picked := map[string]bool{}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--server" && i+1 < len(args):
			i++
			server = args[i]
		case a == "--dry-run":
			dryRun = true
		case a == "--yes":
			yes = true
		case slices.Contains(claudesync.Folders, a):
			picked[a] = true
		default:
			fmt.Fprintf(sio.Err, "bx claude sync: unknown argument %q\n%s\n", a, syncUsage)
			return 2
		}
	}
	var folders []string
	for _, f := range claudesync.Folders {
		if picked[f] || len(picked) == 0 {
			folders = append(folders, f)
		}
	}
	if server == "" {
		fmt.Fprintln(sio.Err, "bx: no server. Pass --server http://100.x.y.z:7357 or set [client] server in", cfg.Root+"/config.toml")
		return 2
	}
	src := claude.ConfigDir()
	if src == "" {
		return fail("no Claude config dir (set CLAUDE_CONFIG_DIR or HOME)")
	}
	files, err := claudesync.Scan(src, folders)
	if err != nil {
		return fail("%v", err)
	}
	byPath := map[string]claudesync.File{}
	manifest := make([]api.SyncFile, 0, len(files))
	paths := make([]string, 0, len(files))
	for _, f := range files {
		if f.Size > claudesync.MaxFile {
			return fail("%s: %d bytes, over 1 MB", f.Src, f.Size)
		}
		byPath[f.Path] = f
		manifest = append(manifest, api.SyncFile{Path: f.Path, SHA256: f.SHA256, Size: f.Size, Exec: f.Exec})
		paths = append(paths, f.Path)
	}

	ctx := context.Background()
	a := client.NewAPI(server)
	plan, err := a.SyncPlan(ctx, api.SyncPlanRequest{Folders: folders, Files: manifest})
	if err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(sio.Out, "bx claude sync -> %s  (server: %s) · %s\n", server, plan.Dir, strings.Join(folders, ", "))
	for _, l := range []struct {
		mark  string
		paths []string
	}{{"+", plan.Add}, {"~", plan.Update}, {"-", plan.Delete}} {
		for _, p := range l.paths {
			fmt.Fprintf(sio.Out, "  %s %s\n", l.mark, p)
		}
	}
	fmt.Fprintf(sio.Out, "  = %d unchanged · %d server-only kept\n", plan.Unchanged, plan.Kept)
	changes := len(plan.Add) + len(plan.Update) + len(plan.Delete)
	if changes == 0 {
		fmt.Fprintln(sio.Out, "Up to date.")
		return 0
	}
	send := slices.Concat(plan.Add, plan.Update)
	var total int64
	var largest claudesync.File
	for _, p := range send {
		f := byPath[p]
		total += f.Size
		if f.Size > largest.Size {
			largest = f
		}
	}
	if total > claudesync.MaxTotal {
		return fail("%d bytes to send, over 20 MB (largest: %s, %d bytes)", total, largest.Src, largest.Size)
	}
	if dryRun {
		return 0
	}
	if !yes {
		if !sio.TTY {
			return fail("not a terminal; pass --yes to apply")
		}
		fmt.Fprintf(sio.Out, "Apply %d changes? [y/N] ", changes)
		line, _ := bufio.NewReader(sio.In).ReadString('\n')
		if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
			fmt.Fprintln(sio.Err, "Cancelled")
			return 1
		}
	}
	req := api.SyncApplyRequest{Folders: folders, Paths: paths, Files: []api.SyncFile{}, Delete: plan.Delete}
	for _, p := range send {
		f, ok := byPath[p]
		if !ok {
			return fail("%s: not in the Mac's files", p)
		}
		b, err := os.ReadFile(f.Src)
		if err != nil {
			return fail("%v", err)
		}
		if len(b) > claudesync.MaxFile {
			return fail("%s: %d bytes, over 1 MB", f.Src, len(b))
		}
		req.Files = append(req.Files, api.SyncFile{Path: p, Exec: f.Exec, Content: b})
	}
	res, err := a.SyncApply(ctx, req)
	if err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(sio.Out, "Synced: %d added, %d updated, %d removed. New Claude sessions will see the changes; resume running ones to pick them up.\n", res.Added, res.Updated, res.Deleted)
	return 0
}
