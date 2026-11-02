package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestRunUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"frobnicate"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if got := errOut.String(); got != "bx: unknown command \"frobnicate\"\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"version"}, &out, &errOut)
	if code != 0 || out.String() != "bx "+Version+"\n" {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestRunHelpListsCommands(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"help"}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d", code)
	}
	for _, name := range []string{"client", "serve", "notify", "hooks"} {
		if !bytes.Contains(out.Bytes(), []byte("  "+name)) {
			t.Errorf("help does not list %q:\n%s", name, out.String())
		}
	}
}

func TestNoArgsRunsTheClient(t *testing.T) {
	var got []string
	Register(Command{Name: "client", Help: "x", Run: func(args []string, _, _ io.Writer) int {
		got = args
		return 7
	}})
	if code := Run(nil, io.Discard, io.Discard); code != 7 || len(got) != 0 {
		t.Fatalf("code=%d args=%q", code, got)
	}
	if code := Run([]string{"--project", "lab"}, io.Discard, io.Discard); code != 7 || strings.Join(got, " ") != "--project lab" {
		t.Fatalf("flags must go to the client: code=%d args=%q", code, got)
	}
}
