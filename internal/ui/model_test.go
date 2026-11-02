package ui

import (
	"strings"
	"testing"
)

func TestRowsOrderAndKinds(t *testing.T) {
	m, _ := sampleModel()
	var got []string
	for _, r := range m.Rows() {
		switch r.Kind {
		case RowStory:
			got = append(got, "story:"+r.Story)
		case RowSession:
			got = append(got, "sess:"+itoa(r.Num))
		}
	}
	want := "story:,sess:6,sess:7,story:fstrim-cron,story:guest-vlan-fix,sess:1,sess:2,sess:3,story:ups-nut-alerts,sess:4,story:zigbee-map-docs"
	if strings.Join(got, ",") != want {
		t.Fatalf("rows:\n got %s\nwant %s", strings.Join(got, ","), want)
	}
}

func TestInitialSelectionIsFirstStorySession(t *testing.T) {
	m, _ := sampleModel()
	if r := m.Selected(); r.Kind != RowSession || r.Num != 1 {
		t.Fatalf("selected = %+v", r)
	}
}

func TestMoveClampsAndFoldKeepsSelectionOnStory(t *testing.T) {
	m, _ := sampleModel()
	for i := 0; i < 50; i++ {
		m.Move(1)
	}
	if r := m.Selected(); r.Kind != RowStory || r.Story != "zigbee-map-docs" {
		t.Fatalf("bottom = %+v", r)
	}
	for i := 0; i < 50; i++ {
		m.Move(-1)
	}
	if r := m.Selected(); r.Kind != RowStory || r.Story != "" {
		t.Fatalf("top = %+v", r)
	}
	m.SelectStory("ups-nut-alerts")
	m.ToggleFold()
	if !m.Folded("ups-nut-alerts") {
		t.Fatal("story should be folded")
	}
	if r := m.Selected(); r.Kind != RowStory || r.Story != "ups-nut-alerts" {
		t.Fatalf("selection moved: %+v", r)
	}
}

func TestWaitingStoryCannotFold(t *testing.T) {
	m, _ := sampleModel()
	m.SelectStory("guest-vlan-fix")
	m.ToggleFold()
	if m.IsOpen("guest-vlan-fix") != true {
		t.Fatal("a story with a waiting session stays open")
	}
	m.SetFolded("zigbee-map-docs", true)
	if m.IsOpen("zigbee-map-docs") {
		t.Fatal("zigbee-map-docs has no waiting session and is folded")
	}
}

func TestFoldAllToggles(t *testing.T) {
	m, p := sampleModel()
	m.FoldAll()
	for name := range p.Stories {
		if !m.Folded(name) {
			t.Errorf("%s not folded", name)
		}
	}
	if !m.IsOpen("guest-vlan-fix") || m.IsOpen("zigbee-map-docs") {
		t.Error("a folded story with a waiting session stays open; the others close")
	}
	m.FoldAll()
	for name := range p.Stories {
		if m.Folded(name) {
			t.Errorf("%s still folded", name)
		}
	}
}

func TestStateText(t *testing.T) {
	p, live := sampleHomeNetwork()
	m := NewModel(p)
	m.SetLive(live)
	cases := map[int]string{
		1: "running",
		2: "asks: permission prompt",
		3: "idle",
		5: "done · r to resume",
		6: "idle",
		7: "idle",
	}
	for num, want := range cases {
		if got := m.StateText(num); got != want {
			t.Errorf("[%d] = %q want %q", num, got, want)
		}
	}
	live.Agents["u3"] = live.Agents["u1"]
	delete(live.Agents, "u1")
	m.SetLive(live)
	if got := m.StateText(1); got != "gone · r to resume" {
		t.Errorf("gone = %q", got)
	}
}

func TestStartingIsNotGone(t *testing.T) {
	p, live := sampleHomeNetwork()
	delete(live.Agents, "u3")
	m := NewModel(p)
	m.SetLive(live)
	if !m.Gone(3) {
		t.Fatal("without states a session without an agent is gone")
	}
	m.SetStates(map[int]string{3: "starting"})
	if m.Gone(3) || m.StateText(3) != "starting" || m.Dot(3) != "◌" {
		t.Fatalf("gone=%v text=%q dot=%q", m.Gone(3), m.StateText(3), m.Dot(3))
	}
}

func TestAlive(t *testing.T) {
	p, live := sampleHomeNetwork()
	m := NewModel(p)
	m.SetLive(live)
	if !m.Alive(1) || !m.Alive(3) || m.Alive(5) || m.Alive(99) {
		t.Fatalf("without states: 1=%v 3=%v 5(done)=%v 99=%v", m.Alive(1), m.Alive(3), m.Alive(5), m.Alive(99))
	}
	delete(live.Agents, "u1")
	m.SetLive(live)
	if m.Alive(1) {
		t.Fatal("a gone session is not alive")
	}
	m.SetStates(map[int]string{1: "starting", 3: "gone", 5: "running"})
	if !m.Alive(1) || m.Alive(3) || m.Alive(5) {
		t.Fatalf("with states: 1=%v 3=%v 5(done)=%v", m.Alive(1), m.Alive(3), m.Alive(5))
	}
}

func TestCounts(t *testing.T) {
	m, _ := sampleModel()
	run, wait := m.Counts()
	if run != 2 || wait != 1 {
		t.Fatalf("run=%d wait=%d", run, wait)
	}
	if s := m.Summary("guest-vlan-fix"); s != "◆1 ●1 ○1" {
		t.Fatalf("summary = %q", s)
	}
	if s := m.Summary("zigbee-map-docs"); s != "✓1" {
		t.Fatalf("summary = %q", s)
	}
}

func TestNextWaiting(t *testing.T) {
	m, _ := sampleModel()
	if n := m.NextWaiting(); n != 2 {
		t.Fatalf("next waiting = %d", n)
	}
}

func itoa(n int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + fmtInt(n)) }
func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestSetProjectKeepsTheCursorRowAndFolds(t *testing.T) {
	m, _ := sampleModel()
	m.SelectSession(4)
	p2, _ := sampleHomeNetwork()
	delete(p2.Stories, "fstrim-cron") // sorts before the selected row: indexes shift
	m.SetProject(p2)
	if n, _ := m.SelectedSession(); n != 4 {
		t.Fatalf("cursor moved to %+v", m.Selected())
	}
	if !m.Folded("zigbee-map-docs") {
		t.Fatal("folds must survive a new snapshot")
	}
}

func TestShellRows(t *testing.T) {
	m, _ := sampleModel()
	m.SetShells([]Shell{{Story: "ups-nut-alerts", K: 3}, {Story: "ups-nut-alerts", K: 1, Command: "zsh"}, {Story: "", K: 1}}, nil)
	var got []string
	for _, r := range m.Rows() {
		switch r.Kind {
		case RowStory:
			got = append(got, "story:"+r.Story)
		case RowSession:
			got = append(got, "sess:"+itoa(r.Num))
		case RowShell:
			got = append(got, "sh:"+r.Story+"$"+itoa(r.K))
		}
	}
	want := "story:,sess:6,sess:7,sh:$1,story:fstrim-cron,story:guest-vlan-fix,sess:1,sess:2,sess:3,story:ups-nut-alerts,sess:4,sh:ups-nut-alerts$1,sh:ups-nut-alerts$3,story:zigbee-map-docs"
	if strings.Join(got, ",") != want {
		t.Fatalf("rows:\n got %s\nwant %s", strings.Join(got, ","), want)
	}

	m.SelectShell("ups-nut-alerts", 3)
	m.SetShells([]Shell{{Story: "ups-nut-alerts", K: 1}, {Story: "ups-nut-alerts", K: 3}}, nil) // the chats shell went away: indexes shift
	if sh, ok := m.SelectedShell(); !ok || sh.Story != "ups-nut-alerts" || sh.K != 3 {
		t.Fatalf("cursor moved to %+v", m.Selected())
	}
	if k := m.NextShell("ups-nut-alerts"); k != 2 {
		t.Fatalf("next shell = %d, want 2", k)
	}
	if k := m.NextShell(""); k != 1 {
		t.Fatalf("next chats shell = %d, want 1", k)
	}

	m.SetFolded("ups-nut-alerts", true)
	for _, r := range m.Rows() {
		if r.Kind == RowShell {
			t.Fatalf("a folded story hides its shells: %+v", r)
		}
	}
	m.SelectShell("ups-nut-alerts", 1)
	if m.Folded("ups-nut-alerts") {
		t.Fatal("SelectShell unfolds the story")
	}

	m.SetShells([]Shell{{Story: "ups-nut-alerts", K: 1}, {Story: "ups-nut-alerts", K: 3}, {Story: "ups-nut-alerts", K: 4}}, nil)
	m.SelectAfterShell("ups-nut-alerts", 2)
	if r := m.Selected(); r.Kind != RowShell || r.K != 3 {
		t.Fatalf("after $2: %+v, want the next shell $3", r)
	}
	m.SetShells([]Shell{{Story: "ups-nut-alerts", K: 1}, {Story: "ups-nut-alerts", K: 3}}, nil)
	m.SelectAfterShell("ups-nut-alerts", 4)
	if r := m.Selected(); r.Kind != RowShell || r.K != 3 {
		t.Fatalf("after $4: %+v, want the previous shell $3", r)
	}
	m.SetShells(nil, nil)
	m.SelectAfterShell("ups-nut-alerts", 3)
	if r := m.Selected(); r.Kind != RowStory || r.Story != "ups-nut-alerts" {
		t.Fatalf("after the last shell: %+v, want the story header", r)
	}

	m.SetShells(nil, map[string]string{"ups-nut-alerts": "/w/ups"})
	if d := m.Dir("ups-nut-alerts"); d != "/w/ups" {
		t.Fatalf("dir = %q", d)
	}
	if d := m.Dir(""); d != "/root/.bx/projects/home-network" {
		t.Fatalf("chats dir falls back to the project path, got %q", d)
	}
}
