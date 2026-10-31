package tmux

import "strings"

// Fake records calls and answers from Replies (key = args joined by one space).
type Fake struct {
	Calls   [][]string
	Replies map[string]string
	Err     error
}

// Run implements Exec.
func (f *Fake) Run(args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
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
