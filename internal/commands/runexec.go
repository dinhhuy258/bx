package commands

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/dinhhuy258/bx/internal/cli"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/runcfg"
)

func init() {
	cli.Register(cli.Command{Name: "run-exec", Help: "internal: run one app of a story in its run shell", Run: runRunExec})
}

type stepT = runcfg.Step

// runStep runs a step to its end, in the pane; execStep replaces bx with it. Tests swap both.
var (
	runStep  = func(s stepT) error { return command(s).Run() }
	execStep = func(s stepT) error {
		bin, err := exec.LookPath(s.Argv[0])
		if err != nil {
			return err
		}
		if err := os.Chdir(s.Dir); err != nil {
			return err
		}
		return syscall.Exec(bin, s.Argv, append(os.Environ(), s.Env...))
	}
)

func command(s stepT) *exec.Cmd {
	c := exec.Command(s.Argv[0], s.Argv[1:]...)
	c.Dir, c.Env = s.Dir, append(os.Environ(), s.Env...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	if s.Stdin != "" {
		c.Stdin = strings.NewReader(s.Stdin)
	} else {
		c.Stdin = os.Stdin
	}
	return c
}

// runRunExec is `bx run-exec [-stop|-setup] <project> <story> <name>`, typed into a run shell
// by bx serve. It builds the image when its Dockerfile changed, runs the setup when it is due
// (or -setup), then becomes the run. -stop runs the run's stop command instead.
func runRunExec(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("run-exec", flag.ContinueOnError)
	fs.SetOutput(errOut)
	stop := fs.Bool("stop", false, "run the stop command")
	again := fs.Bool("setup", false, "run the setup even when it ran")
	if fs.Parse(args) != nil || fs.NArg() != 3 {
		_, _ = fmt.Fprintln(errOut, "usage: bx run-exec [-stop|-setup] <project> <story> <name>")
		return 2
	}
	project, story, name := fs.Arg(0), fs.Arg(1), fs.Arg(2)
	if err := runExec(project, story, name, *stop, *again, out); err != nil {
		_, _ = fmt.Fprintln(errOut, "bx:", err)
		return 1
	}
	return 0
}

func runExec(project, story, name string, stop, again bool, out io.Writer) error {
	cfg, err := config.Load(config.DefaultRoot())
	if err != nil {
		return err
	}
	data, err := os.ReadFile(cfg.AppFile(project))
	if err != nil {
		return err
	}
	f, err := runcfg.Parse(data)
	if err != nil {
		return err
	}
	r, ok := f.Find(name)
	if !ok {
		return fmt.Errorf("no run %q in %s", name, cfg.AppFile(project))
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	o := runcfg.Opts{
		Project: project, Story: story, Root: cfg.WorktreeDir(project, story),
		Host: os.Getenv("BX_APP_HOST"), UID: os.Getuid(), GID: os.Getgid(),
		Cache: cfg.AppCache(project), Shell: shell,
	}
	dir := filepath.Join(o.Root, r.Dir)
	if stop {
		if r.Stop == "" {
			return nil
		}
		v := runcfg.Vars{Host: o.Host, Story: story, Root: o.Root, Dir: dir}
		return execStep(stepT{Argv: []string{shell, "-lc", runcfg.Expand(r.Stop, v)}, Dir: dir})
	}
	steps := runcfg.Plan(r, o)
	if r.Docker() {
		for _, c := range append([]string{"home"}, cacheDirs(r)...) {
			if err := os.MkdirAll(filepath.Join(o.Cache, c), 0o755); err != nil {
				return err
			}
		}
	}
	if b := steps.Build; b != nil && imageHash(b.Tag) != b.Hash {
		show(out, "docker build -t "+b.Tag)
		if err := runStep(b.Step); err != nil {
			return fmt.Errorf("build: %w", err)
		}
	}
	mark := cfg.SetupMark(project, story, name)
	if s := steps.Setup; s != nil && (again || setupDue(r, dir, mark)) {
		show(out, r.Setup.Cmd)
		if err := runStep(*s); err != nil {
			return fmt.Errorf("setup: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(mark), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(mark, nil, 0o644); err != nil {
			return err
		}
	}
	show(out, r.Cmd)
	return execStep(steps.Main)
}

// setupDue: with creates, while that path is missing; without, until the setup ran once.
func setupDue(r runcfg.Run, dir, mark string) bool {
	if r.Creates != "" {
		_, err := os.Stat(filepath.Join(dir, r.Creates))
		return errors.Is(err, os.ErrNotExist)
	}
	_, err := os.Stat(mark)
	return errors.Is(err, os.ErrNotExist)
}

func cacheDirs(r runcfg.Run) []string {
	out := make([]string, len(r.Cache))
	for i, c := range r.Cache {
		out[i] = runcfg.CacheDir(c)
	}
	return out
}

// imageHash is the bx.hash label of a built image, "" when there is none.
var imageHash = func(tag string) string {
	out, err := exec.Command("docker", "image", "inspect", "-f", `{{index .Config.Labels "bx.hash"}}`, tag).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// show prints the command about to run, dimmed, the way a shell would echo it.
func show(out io.Writer, cmd string) { _, _ = fmt.Fprintf(out, "\x1b[2m$ %s\x1b[0m\n", cmd) }
