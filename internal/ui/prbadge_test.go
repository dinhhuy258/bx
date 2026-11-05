package ui

import "testing"

func TestPRBadgeText(t *testing.T) {
	pal := newPalette(true)
	cases := []struct {
		b    PRBadge
		want string
	}{
		{PRBadge{}, ""},
		{PRBadge{Count: 1, Number: 142, Fail: true, Threads: 3, Behind: true}, "#142 ✗ ci 💬 3 ⇄"},
		{PRBadge{Count: 1, Number: 139, Pending: true, Review: true}, "#139 ◌ ci review"},
		{PRBadge{Count: 1, Number: 137, Pass: true, Ready: true}, "#137 ✓ ready"},
		{PRBadge{Count: 1, Number: 131, Merged: 1}, "merged #131"},
		{PRBadge{Count: 1, Number: 120, Closed: 1}, "closed #120"},
		{PRBadge{Count: 2, Fail: true, Threads: 3, Changes: true}, "2 PRs ✗ ci 💬 3 changes"},
		{PRBadge{Count: 2, Merged: 1, Pass: true}, "2 PRs merged 1/2 ✓ ci"},
		{PRBadge{Count: 2, Merged: 2}, "merged 2/2"},
		{PRBadge{Count: 2, Closed: 2}, "closed 2/2"},
		{PRBadge{Count: 2, Merged: 1, Closed: 1}, "merged 1/2"},
		{PRBadge{Count: 1, Number: 9, Conflict: true}, "#9 ⇄ conflict"},
	}
	for _, c := range cases {
		if got := prBadgeText(c.b, pal); got != c.want {
			t.Errorf("%+v: %q, want %q", c.b, got, c.want)
		}
	}
}

func TestRenderStoryRowsShowPRBadges(t *testing.T) {
	m, _ := sampleModel()
	live := m.Live
	live.PR = map[string]PRBadge{
		"guest-vlan-fix":  {Count: 1, Number: 142, Fail: true, Threads: 3, Behind: true},
		"ups-nut-alerts":  {Count: 1, Number: 139, Pending: true, Review: true},
		"zigbee-map-docs": {Count: 1, Number: 137, Pass: true, Ready: true},
	}
	m.SetLive(live)
	m.SetFolded("zigbee-map-docs", true)
	golden(t, "home-pr-64x16", Render(m, 64, 16, RenderOptions{Plain: true}), 64)
	golden(t, "home-pr-46x16", Render(m, 46, 16, RenderOptions{Plain: true}), 46)
}
