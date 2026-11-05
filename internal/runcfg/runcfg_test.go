package runcfg

import (
	"reflect"
	"strings"
	"testing"
)

const sample = `hide = [5173]

[[run]]
name = "api"
cmd = "php artisan serve --port=8000"
dir = "be"
image = "php:8.3-cli"
port = 8000
setup = { cmd = "composer install", image = "composer:2" }
creates = "vendor"
cache = ["/home/bx/.composer"]

[[run]]
name = "web"
cmd = "npm run dev"
dir = "fe"
port = [3000, 3001]
open = "/login"
setup = "npm ci"
stop = "pkill -f vite"
[run.env]
VITE_API = "http://${host}:8000"
`

func TestParse(t *testing.T) {
	f, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	want := File{
		Hide: []int{5173},
		Runs: []Run{
			{Name: "api", Cmd: "php artisan serve --port=8000", Dir: "be", Image: "php:8.3-cli", Port: Ports{8000},
				Setup: Setup{Cmd: "composer install", Image: "composer:2"}, Creates: "vendor", Cache: []string{"/home/bx/.composer"}},
			{Name: "web", Cmd: "npm run dev", Dir: "fe", Port: Ports{3000, 3001}, Open: "/login",
				Setup: Setup{Cmd: "npm ci"}, Stop: "pkill -f vite", Env: map[string]string{"VITE_API": "http://${host}:8000"}},
		},
	}
	if !reflect.DeepEqual(f, want) {
		t.Errorf("got  %+v\nwant %+v", f, want)
	}
}

func TestParseErrors(t *testing.T) {
	_, err := Parse([]byte("[[run]]\nname = \"a\"\ncmd = \n"))
	var se *SyntaxError
	if !asSyntax(err, &se) || se.Line != 3 {
		t.Errorf("syntax: %#v", err)
	}
	if _, err := Parse([]byte("[[run]]\nname = \"a\"\ncmd = \"x\"\nprot = 3\n")); err == nil || !strings.Contains(err.Error(), "prot") {
		t.Errorf("unknown key: %v", err)
	}
	if _, err := Parse([]byte("[[run]]\nport = \"x\"\n")); err == nil {
		t.Error("port string accepted")
	}
}

func TestValidate(t *testing.T) {
	f := File{Runs: []Run{
		{Name: "web", Cmd: "x"},
		{Name: "web", Cmd: ""},
		{Name: "a b", Cmd: "x", Image: "i", Build: "FROM x"},
		{Name: "c", Cmd: "x", Dir: "../up", Port: Ports{0}, Cache: []string{"/c"}},
		{Name: "d", Cmd: "x", Env: map[string]string{"1X": "y"}, Setup: Setup{Image: "i"}},
	}}
	got := Validate(f, nil)
	want := []FieldError{
		{Run: 1, Field: "name", Message: `"web" is taken`},
		{Run: 1, Field: "cmd", Message: "required"},
		{Run: 2, Field: "name", Message: "letters, digits, - and _ only"},
		{Run: 2, Field: "build", Message: "image or build, not both"},
		{Run: 3, Field: "dir", Message: "a folder inside the story"},
		{Run: 3, Field: "port", Message: "1 to 65535"},
		{Run: 3, Field: "cache", Message: "needs image or build"},
		{Run: 4, Field: "env", Message: `"1X" is not a variable name`},
		{Run: 4, Field: "setup", Message: "an image needs a command"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	ws := File{Runs: []Run{{Name: "a", Cmd: "x", Dir: "be/sub"}, {Name: "b", Cmd: "x", Dir: "ops"}, {Name: "c", Cmd: "x"}}}
	got = Validate(ws, []string{"be", "fe"})
	if len(got) != 1 || got[0].Run != 1 || got[0].Message != "one of the repos: be, fe" {
		t.Errorf("workspace: %+v", got)
	}
}

func TestFormatRoundTrip(t *testing.T) {
	f, _ := Parse([]byte(sample))
	f.Runs[0].Image, f.Runs[0].Build = "", "FROM php:8.3-cli\nRUN apt-get update\n"
	out := Format(f)
	back, err := Parse(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !reflect.DeepEqual(back, f) {
		t.Errorf("round trip:\n%s\ngot %+v", out, back)
	}
	if !strings.Contains(string(out), "build = '''\nFROM php:8.3-cli\n") {
		t.Errorf("build is not a literal block:\n%s", out)
	}
}

func TestHasComments(t *testing.T) {
	for in, want := range map[string]bool{
		sample:                                false,
		"# runs\n[[run]]\n":                   true,
		"[[run]]\nname = \"a\" # web\n":       true,
		"[[run]]\ncmd = \"echo '#1'\"\n":      false,
		"build = '''\n# not a comment\n'''\n": false,
	} {
		if got := HasComments([]byte(in)); got != want {
			t.Errorf("HasComments(%q) = %v", in, got)
		}
	}
}
