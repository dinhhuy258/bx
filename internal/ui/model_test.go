package ui

import (
	"strings"
	"testing"
)

func TestRowsOrderAndKinds(t *testing.T) {
	m, _ := sampleModel(Options{AutoExpandWaiting: true})
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
	m, _ := sampleModel(Options{AutoExpandWaiting: true})
	if r := m.Selected(); r.Kind != RowSession || r.Num != 1 {
		t.Fatalf("selected = %+v", r)
	}
}

func TestMoveClampsAndFoldKeepsSelectionOnStory(t *testing.T) {
	m, _ := sampleModel(Options{AutoExpandWaiting: true})
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
	m, _ := sampleModel(Options{AutoExpandWaiting: true})
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
	m, p := sampleModel(Options{AutoExpandWaiting: false})
	m.FoldAll()
	for name := range p.Stories {
		if !m.Folded(name) {
			t.Errorf("%s not folded", name)
		}
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
	m := NewModel(p, Options{AutoExpandWaiting: true})
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
	m := NewModel(p, Options{AutoExpandWaiting: true})
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
	m := NewModel(p, Options{AutoExpandWaiting: true})
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
	m, _ := sampleModel(Options{AutoExpandWaiting: true})
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
	m, _ := sampleModel(Options{AutoExpandWaiting: true})
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
	m, _ := sampleModel(Options{AutoExpandWaiting: true})
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
