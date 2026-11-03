package tmux

import "strings"

// Fake records calls and answers from Replies (key = args joined by one space). Errs, with
// the same keys, makes a command fail; it is checked first and the call is not recorded.
type Fake struct {
	Calls   [][]string
	Inputs  []string // the stdin of every RunInput, in order
	Replies map[string]string
	Errs    map[string]error
	Err     error
}

// Run implements Exec.
func (f *Fake) Run(args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	if err, ok := f.Errs[key]; ok {
		return nil, err
	}
	if r, ok := f.Replies[key]; ok {
		f.Calls = append(f.Calls, args)
		return []byte(r), nil
	}
	// Queries must not pollute the recorded command list.
	if (args[0] == "display-message" && len(args) > 1 && args[1] == "-p") || args[0] == "list-sessions" || args[0] == "list-windows" || args[0] == "list-panes" {
		return nil, errNoReply
	}
	f.Calls = append(f.Calls, args)
	return nil, f.Err
}

type noReply struct{}

func (noReply) Error() string { return "fake: no reply" }

var errNoReply error = noReply{}

// RunInput implements Inputter: it records in, then runs as Run.
func (f *Fake) RunInput(in []byte, args ...string) ([]byte, error) {
	if _, ok := f.Errs[strings.Join(args, " ")]; !ok {
		f.Inputs = append(f.Inputs, string(in))
	}
	return f.Run(args...)
}
