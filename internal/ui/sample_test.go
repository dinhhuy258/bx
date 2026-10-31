package ui

import (
	"time"

	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/state"
)

// sampleHomeNetwork mirrors the 'home-network' project of docs/bx-prototype.html.
func sampleHomeNetwork() (*state.Project, Live) {
	p := state.New("home-network", "/root/.bx/projects/home-network")
	p.Stories["guest-vlan-fix"] = &state.Story{Branch: "feat/guest-vlan-fix"}
	p.Stories["ups-nut-alerts"] = &state.Story{Branch: "feat/ups-nut-alerts"}
	p.Stories["zigbee-map-docs"] = &state.Story{Branch: "docs/zigbee-map"}
	p.Stories["fstrim-cron"] = &state.Story{Branch: "chore/fstrim-cron"}
	now := time.Date(2026, 10, 30, 14, 32, 0, 0, time.UTC)
	add := func(story, uuid, title string, age time.Duration, done bool) {
		p.AddSession(&state.Session{Story: story, UUID: uuid, Title: title, Done: done, Created: now.Add(-age)})
	}
	add("guest-vlan-fix", "u1", "Fix guest DHCP lease time", 6*time.Minute, false)         // [1] running
	add("guest-vlan-fix", "u2", "Write the test checklist", 2*time.Minute, false)          // [2] waiting
	add("guest-vlan-fix", "u3", "Shorter lease for the IoT VLAN", time.Hour, false)        // [3] idle
	add("ups-nut-alerts", "u4", "Alert when the UPS is on battery", 18*time.Minute, false) // [4] running
	add("zigbee-map-docs", "u5", "Document the Zigbee mesh map", 3*time.Hour, true)        // [5] done
	add("", "u6", "What does pmxcfs do?", 5*time.Minute, false)                            // [6] idle chat
	add("", "u7", "Explain the fasttrack rule", 40*time.Minute, false)                     // [7] idle chat, edited 2
	live := Live{
		Now: now,
		Agents: map[string]claude.Agent{
			"u1": {SessionID: "u1", Status: "busy"},
			"u2": {SessionID: "u2", Status: "waiting", WaitingFor: "permission prompt"},
			"u3": {SessionID: "u3", Status: "idle"},
			"u4": {SessionID: "u4", Status: "busy"},
			"u6": {SessionID: "u6", Status: "idle"},
			"u7": {SessionID: "u7", Status: "idle"},
		},
		Diff:       map[string]Diff{"guest-vlan-fix": {42, 7}, "ups-nut-alerts": {31, 2}, "zigbee-map-docs": {58, 0}},
		DirtyFiles: 2,
		Branch:     "main",
	}
	return p, live
}

// sampleModel is the sample with zigbee-map-docs folded, as in the prototype.
func sampleModel(opt Options) (*Model, *state.Project) {
	p, live := sampleHomeNetwork()
	m := NewModel(p, opt)
	m.SetFolded("zigbee-map-docs", true)
	m.SetLive(live)
	return m, p
}
