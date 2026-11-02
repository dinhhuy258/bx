package client

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/claudesync"
	"github.com/dinhhuy258/bx/internal/ui"
)

// noSync is shown when the server is older than the sync endpoints.
const noSync = "bx serve doesn't support sync yet. Update the server: make deploy"

type (
	// syncPlanMsg is a scan of the Mac and the server's plan for it.
	syncPlanMsg struct {
		seq  int
		dir  string
		mac  claudesync.MacScan
		plan api.SyncPlan
		err  error
	}
	// syncAppliedMsg is the reply of a sync.
	syncAppliedMsg struct {
		seq int
		res api.SyncResult
		err error
	}
)

// scanSync scans the Mac's Claude dir and asks the server for a plan, off the Update loop.
// Each scan or sync bumps syncSeq, so a late reply of an older one is dropped.
func (m *Model) scanSync() tea.Cmd {
	m.syncSeq++
	dir := claude.ConfigDir()
	m.settings.SyncScanning(dir)
	be, ctx, seq := m.be, m.ctx, m.syncSeq
	return func() tea.Msg {
		if dir == "" {
			return syncPlanMsg{seq: seq, err: errors.New("no Claude config dir: set HOME or CLAUDE_CONFIG_DIR")}
		}
		mac, err := claudesync.Scan(dir)
		if err != nil {
			return syncPlanMsg{seq: seq, err: fmt.Errorf("scan %s: %w", dir, err)}
		}
		files := make([]api.SyncFile, 0, len(mac.Files))
		for _, f := range mac.Files {
			files = append(files, api.SyncFile{Path: f.Path, SHA256: f.SHA256, Exec: f.Exec})
		}
		plan, err := be.SyncPlan(ctx, api.SyncPlanRequest{Files: files})
		return syncPlanMsg{seq: seq, dir: dir, mac: mac, plan: plan, err: err}
	}
}

// syncError is the text of a failed scan, plan or sync.
func syncError(err error) string {
	var se *StatusError
	if errors.As(err, &se) && (se.Code == http.StatusNotFound || se.Code == http.StatusMethodNotAllowed) {
		return noSync
	}
	return err.Error()
}

// gotSyncPlan shows the plan in the sync list. A file over the size limit makes its item an
// error, so it can't be selected.
func (m *Model) gotSyncPlan(msg syncPlanMsg) {
	if m.settings == nil || msg.seq != m.syncSeq {
		return
	}
	if msg.err != nil {
		m.settings.SyncFailed(syncError(msg.err))
		return
	}
	m.syncMac, m.syncPlan = msg.mac, msg.plan
	big := map[string]string{}
	for _, f := range msg.mac.Files {
		if it := claudesync.ItemOf(f.Path); f.Size > claudesync.MaxFile && big[it] == "" {
			big[it] = fmt.Sprintf("%s: %s, over the 1 MB limit", f.Path, megabytes(f.Size))
		}
	}
	d := ui.SyncData{MacDir: msg.dir, ServerDir: msg.plan.Dir, At: time.Now(), Done: m.syncDone}
	seen := map[string]bool{}
	for _, it := range msg.plan.Items {
		if it.Status == claudesync.StatusServerOnly {
			d.ServerOnly++
			continue
		}
		seen[it.Name] = true
		item := ui.SyncItem{Name: it.Name, Status: it.Status, Error: it.Error, Warning: msg.mac.Warnings[it.Name], Link: msg.mac.Links[it.Name]}
		if item.Error == "" {
			item.Error = big[it.Name]
		}
		d.Items = append(d.Items, item)
	}
	for it, w := range msg.mac.Warnings { // an item whose every file was skipped
		if !seen[it] {
			d.Items = append(d.Items, ui.SyncItem{Name: it, Warning: w, Link: msg.mac.Links[it]})
		}
	}
	sort.Slice(d.Items, func(i, j int) bool { return d.Items[i].Name < d.Items[j].Name })
	m.settings.SetSync(d, m.opened.SyncExclude)
}

func megabytes(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }

// applySync syncs the checked items: it reads the Mac files of their adds and updates and
// sends them with their deletes, off the Update loop.
func (m *Model) applySync() tea.Cmd {
	scope := m.settings.SyncScope()
	inScope := map[string]bool{}
	for _, it := range scope {
		inScope[it] = true
	}
	src := map[string]claudesync.File{}
	req := api.SyncApplyRequest{Items: scope, Paths: []string{}, Delete: []string{}}
	for _, f := range m.syncMac.Files {
		if inScope[claudesync.ItemOf(f.Path)] {
			req.Paths = append(req.Paths, f.Path)
			src[f.Path] = f
		}
	}
	var writes []claudesync.File
	for _, it := range m.syncPlan.Items {
		if !inScope[it.Name] {
			continue
		}
		req.Delete = append(req.Delete, it.Delete...)
		for _, p := range append(append([]string{}, it.Add...), it.Update...) {
			if f, ok := src[p]; ok {
				writes = append(writes, f)
			}
		}
	}
	m.syncSeq++
	m.settings.SyncApplying()
	be, ctx, seq := m.be, m.ctx, m.syncSeq
	return func() tea.Msg {
		var total int64
		var largest claudesync.File
		for _, f := range writes {
			b, err := os.ReadFile(f.Src)
			if err != nil {
				return syncAppliedMsg{seq: seq, err: err}
			}
			if len(b) > claudesync.MaxFile {
				return syncAppliedMsg{seq: seq, err: fmt.Errorf("%s: %s, over the 1 MB limit", f.Path, megabytes(int64(len(b))))}
			}
			total += int64(len(b))
			if f.Size = int64(len(b)); f.Size > largest.Size {
				largest = f
			}
			req.Files = append(req.Files, api.SyncFile{Path: f.Path, Exec: f.Exec, Content: b})
		}
		if total > claudesync.MaxTotal {
			return syncAppliedMsg{seq: seq, err: fmt.Errorf("%s to sync, over the 20 MB limit; the largest file is %s (%s)", megabytes(total), largest.Path, megabytes(largest.Size))}
		}
		res, err := be.SyncApply(ctx, req)
		return syncAppliedMsg{seq: seq, res: res, err: err}
	}
}

// gotSyncApplied reports a sync and scans again, so the list shows the server as it is now.
func (m *Model) gotSyncApplied(msg syncAppliedMsg) tea.Cmd {
	if m.settings == nil || msg.seq != m.syncSeq {
		return nil
	}
	if msg.err != nil {
		m.settings.SyncFailed(syncError(msg.err))
		return nil
	}
	m.setFlash(fmt.Sprintf("✓ synced: %d added, %d updated, %d removed", msg.res.Added, msg.res.Updated, msg.res.Deleted))
	m.syncDone = true
	return m.scanSync()
}

// saveSyncExclude remembers the unselected items in client.json.
func (m *Model) saveSyncExclude() {
	m.opened.SyncExclude = m.settings.SyncExclude()
	if err := m.opened.save(m.cfg.Root); err != nil {
		m.setFlash("save sync selection: " + err.Error())
	}
}
