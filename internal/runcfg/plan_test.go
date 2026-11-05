package runcfg

import (
	"reflect"
	"strings"
	"testing"
)

var opts = Opts{
	Project: "unem", Story: "s1", Root: "/w/unem/s1", Host: "dev",
	UID: 1000, GID: 1000, Cache: "/r/cache/unem", Shell: "/bin/zsh",
}

func TestExpand(t *testing.T) {
	v := Vars{Host: "dev", Story: "s1", Root: "/w/s1", Dir: "/w/s1/fe"}
	if got := Expand("http://${host}:8000/${story} ${root} ${dir} ${other}", v); got != "http://dev:8000/s1 /w/s1 /w/s1/fe ${other}" {
		t.Errorf("%q", got)
	}
}

func TestPlanHost(t *testing.T) {
	r := Run{Name: "web", Cmd: "npm run dev", Dir: "fe", Setup: Setup{Cmd: "npm ci"},
		Env: map[string]string{"VITE_API": "http://${host}:8000"}}
	p := Plan(r, opts)
	if p.Build != nil {
		t.Error("build on the host")
	}
	want := Step{Argv: []string{"/bin/zsh", "-lc", "npm ci"}, Dir: "/w/unem/s1/fe", Env: []string{"VITE_API=http://dev:8000"}}
	if p.Setup == nil || !reflect.DeepEqual(*p.Setup, want) {
		t.Errorf("setup: %+v", p.Setup)
	}
	want.Argv = []string{"/bin/zsh", "-lc", "npm run dev"}
	if !reflect.DeepEqual(p.Main, want) {
		t.Errorf("main: %+v", p.Main)
	}
}

func TestPlanDocker(t *testing.T) {
	r := Run{Name: "api", Cmd: "php artisan serve", Dir: "be", Image: "php:8.3-cli",
		Setup: Setup{Cmd: "composer install", Image: "composer:2"}, Cache: []string{"/home/bx/.composer"},
		Env: map[string]string{"B": "2", "A": "${story}"}}
	p := Plan(r, opts)
	main := strings.Join(p.Main.Argv, " ")
	want := "docker run --rm -it --init --network host --name bx-unem-s1-api" +
		" --label bx.project=unem --label bx.story=s1 --label bx.run=api --user 1000:1000" +
		" -e HOME=/home/bx -v /r/cache/unem/home:/home/bx -v /r/cache/unem/home_bx_.composer:/home/bx/.composer" +
		" -v /w/unem/s1:/work -w /work/be -e A=s1 -e B=2 php:8.3-cli sh -lc php artisan serve"
	if main != want {
		t.Errorf("main:\n%s\nwant\n%s", main, want)
	}
	setup := strings.Join(p.Setup.Argv, " ")
	if !strings.Contains(setup, "--name bx-unem-s1-api-setup") || !strings.HasSuffix(setup, "composer:2 sh -lc composer install") {
		t.Errorf("setup: %s", setup)
	}
}

func TestPlanBuild(t *testing.T) {
	r := Run{Name: "api", Cmd: "x", Build: "FROM php:8.3-cli\n"}
	p := Plan(r, opts)
	if p.Build == nil || p.Build.Tag != "bx/unem-api" || p.Build.Hash == "" {
		t.Fatalf("build: %+v", p.Build)
	}
	if got := strings.Join(p.Build.Argv, " "); got != "docker build -t bx/unem-api --label bx.hash="+p.Build.Hash+" -f - /w/unem/s1" {
		t.Errorf("argv: %s", got)
	}
	if p.Build.Stdin != r.Build || !strings.Contains(strings.Join(p.Main.Argv, " "), " bx/unem-api sh -lc x") {
		t.Errorf("stdin %q main %v", p.Build.Stdin, p.Main.Argv)
	}
}
