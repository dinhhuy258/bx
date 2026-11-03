package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Output runs git in dir and returns its stdout alone: warnings on stderr (CRLF, a missing
// index) must not end up in a diff. stderr goes into the error. env (KEY=value) is added to
// the environment.
func (g *Git) Output(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, g.Bin, args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), env...)
	cmd.WaitDelay = time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), ctx.Err())
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// MergeBase is the commit HEAD and base share, the point a story's diff starts from.
func (g *Git) MergeBase(ctx context.Context, dir, base string) (string, error) {
	out, err := g.Output(ctx, dir, nil, "merge-base", "HEAD", base)
	return strings.TrimSpace(string(out)), err
}
