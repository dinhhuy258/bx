package runcfg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Vars fill ${host}, ${story}, ${root} and ${dir} in cmd, setup, stop and env values.
type Vars struct{ Host, Story, Root, Dir string }

// Expand replaces the bx variables in s; other ${...} stay for the shell.
func Expand(s string, v Vars) string {
	return strings.NewReplacer("${host}", v.Host, "${story}", v.Story, "${root}", v.Root, "${dir}", v.Dir).Replace(s)
}

// Opts are where a run runs: the story folder (Root), the run host, the user the containers
// run as, the project's cache folder (~/.bx/cache/<project>) and the login shell on the host.
type Opts struct {
	Project, Story, Root, Host string
	UID, GID                   int
	Cache                      string
	Shell                      string
}

// Step is one command: argv, its folder, env added to bx's, and stdin ("" none).
type Step struct {
	Argv  []string
	Dir   string
	Env   []string
	Stdin string
}

// BuildStep builds a run's image when its Dockerfile changed: Hash is the bx.hash label.
type BuildStep struct {
	Step
	Tag, Hash string
}

// Steps are what run-exec does: build (nil when none), setup (nil when none; run-exec decides
// if it is due), then exec Main.
type Steps struct {
	Build *BuildStep
	Setup *Step
	Main  Step
}

// Plan turns r into Steps.
func Plan(r Run, o Opts) Steps {
	dir := filepath.Join(o.Root, r.Dir)
	v := Vars{Host: o.Host, Story: o.Story, Root: o.Root, Dir: dir}
	env := make([]string, 0, len(r.Env))
	for _, k := range slices.Sorted(maps.Keys(r.Env)) {
		env = append(env, k+"="+Expand(r.Env[k], v))
	}
	var s Steps
	image := r.Image
	if r.Build != "" {
		sum := sha256.Sum256([]byte(r.Build))
		image = "bx/" + o.Project + "-" + r.Name
		hash := hex.EncodeToString(sum[:8])
		s.Build = &BuildStep{
			Step: Step{Argv: []string{"docker", "build", "-t", image, "--label", "bx.hash=" + hash, "-f", "-", dir}, Dir: dir, Stdin: r.Build},
			Tag:  image, Hash: hash,
		}
	}
	step := func(name, img, cmd string) Step {
		cmd = Expand(cmd, v)
		if img == "" {
			return Step{Argv: []string{o.Shell, "-lc", cmd}, Dir: dir, Env: env}
		}
		return Step{Argv: DockerArgs(o, r, name, img, cmd, env), Dir: dir}
	}
	if r.Setup.Cmd != "" {
		img := image
		if r.Setup.Image != "" {
			img = r.Setup.Image
		}
		st := step(containerName(o, r.Name)+"-setup", img, r.Setup.Cmd)
		s.Setup = &st
	}
	s.Main = step(containerName(o, r.Name), image, r.Cmd)
	return s
}

func containerName(o Opts, run string) string {
	return strings.NewReplacer(".", "_", ":", "_").Replace("bx-" + o.Project + "-" + o.Story + "-" + run)
}

// DockerArgs is `docker run` for one step: host network (no port mapping, the same port as
// on the host), the bx labels port detection reads, the host user with a home in the cache,
// the cache paths, and the story folder at /work.
func DockerArgs(o Opts, r Run, name, image, cmd string, env []string) []string {
	home := filepath.Join(o.Cache, "home")
	a := []string{"docker", "run", "--rm", "-it", "--init", "--network", "host", "--name", name,
		"--label", "bx.project=" + o.Project, "--label", "bx.story=" + o.Story, "--label", "bx.run=" + r.Name,
		"--user", fmt.Sprintf("%d:%d", o.UID, o.GID),
		"-e", "HOME=/home/bx", "-v", home + ":/home/bx"}
	for _, c := range r.Cache {
		a = append(a, "-v", filepath.Join(o.Cache, CacheDir(c))+":"+c)
	}
	a = append(a, "-v", o.Root+":/work", "-w", path.Join("/work", filepath.ToSlash(r.Dir)))
	for _, e := range env {
		a = append(a, "-e", e)
	}
	return append(a, image, "sh", "-lc", cmd)
}

// CacheDir is the folder under the project's cache that keeps container path c.
func CacheDir(c string) string {
	return "home_" + strings.ReplaceAll(strings.TrimPrefix(strings.TrimPrefix(c, "/home/"), "/"), "/", "_")
}
