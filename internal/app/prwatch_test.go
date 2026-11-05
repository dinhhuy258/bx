package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestPRLookRunsOneGraphQLCall(t *testing.T) {
	raw, err := os.ReadFile("../prwatch/testdata/graphql.json")
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	a := &App{GH: func(_ context.Context, dir string, args ...string) (string, error) {
		calls = append(calls, append([]string{dir}, args...))
		return string(raw), nil
	}}
	me, found, err := a.PRLook(context.Background(), "/wt", "acme/netcfg", []string{"guest-vlan-fix", "old"})
	if err != nil {
		t.Fatal(err)
	}
	if me != "huy" || len(found) != 2 || len(calls) != 1 || calls[0][0] != "/wt" || calls[0][1] != "api" || calls[0][2] != "graphql" {
		t.Fatalf("me %q found %d calls %v", me, len(found), calls)
	}
	if !strings.Contains(calls[0][4], `owner: "acme", name: "netcfg"`) {
		t.Fatalf("query = %s", calls[0][4])
	}
}

func TestFailedLog(t *testing.T) {
	a := &App{GH: func(_ context.Context, dir string, args ...string) (string, error) {
		if strings.Join(args, " ") != "run view 9 --log-failed" {
			t.Fatalf("args = %v", args)
		}
		return "a\nb\n", nil
	}}
	if got, err := a.FailedLog(context.Background(), "/wt", 9); err != nil || got != "a\nb\n" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestPRLookErrorIsShort(t *testing.T) {
	a := &App{GH: func(_ context.Context, dir string, args ...string) (string, error) {
		return "", errors.New("gh " + strings.Join(args, " ") + ": HTTP 401: Bad credentials\n(https://api.github.com/graphql)")
	}}
	_, _, err := a.PRLook(context.Background(), "/wt", "acme/netcfg", []string{"b"})
	if err == nil || err.Error() != "gh: HTTP 401: Bad credentials (https://api.github.com/graphql)" {
		t.Fatalf("err = %v", err)
	}
}
