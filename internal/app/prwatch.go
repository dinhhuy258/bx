package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dinhhuy258/bx/internal/prwatch"
)

// PRLook asks GitHub, as the account of dir, for the PRs of branches in the repository slug
// ("owner/name"): one GraphQL call.
func (a *App) PRLook(ctx context.Context, dir, slug string, branches []string) (string, []prwatch.Found, error) {
	owner, name, ok := strings.Cut(slug, "/")
	if !ok {
		return "", nil, fmt.Errorf("not a GitHub repository: %q", slug)
	}
	args := []string{"api", "graphql", "-f", "query=" + prwatch.Query(owner, name, branches)}
	out, err := a.GH(ctx, dir, args...)
	if err != nil {
		return "", nil, ghShort(err, args)
	}
	return prwatch.Parse([]byte(out), len(branches))
}

// FailedLog is the log of the failed steps of an Actions run.
func (a *App) FailedLog(ctx context.Context, dir string, run int64) (string, error) {
	return a.GH(ctx, dir, "run", "view", strconv.FormatInt(run, 10), "--log-failed")
}

// ghShort is a gh failure without the command line, which for a GraphQL call is the whole
// query, cut to one line of at most 200 characters: it is shown on the PR screen.
func ghShort(err error, args []string) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.New("gh: timed out")
	}
	msg := strings.TrimPrefix(err.Error(), "gh "+strings.Join(args, " ")+": ")
	msg = strings.Join(strings.Fields(msg), " ")
	if r := []rune(msg); len(r) > 200 {
		msg = string(r[:199]) + "…"
	}
	return errors.New("gh: " + msg)
}
