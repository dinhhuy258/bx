package client

// Target is what the pane streams: Claude session Num, or shell K of Story ("" = chats).
type Target struct {
	Num   int
	Story string
	K     int
}

// sessionTarget is the target of Claude session num.
func sessionTarget(num int) Target { return Target{Num: num} }

// shellTarget is the target of shell k of story.
func shellTarget(story string, k int) Target { return Target{Story: story, K: k} }

// Shell reports whether the target is a shell.
func (t Target) Shell() bool { return t.K != 0 }

// Zero reports whether there is no target: the pane is empty.
func (t Target) Zero() bool { return t == Target{} }
