package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
	"github.com/kxn/codex-remote-feishu/internal/testutil"
)

// After restart the child bot can be detached while the parent and prewarm
// runtimes already report global thread lists. Listing must not require a
// running child instance in order to recover that child's saved sessions.
func TestPersistedWorkspaceRecoveryDetachedWithoutChildRuntime(t *testing.T) {
	now := time.Date(2026, 10, 22, 8, 22, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	parent := t.TempDir()
	child := filepath.Join(parent, "memory")
	pool := t.TempDir()
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []state.ThreadRecord{
		{ThreadID: "migrated", Name: "Memory", CWD: child, Loaded: true, LastUsedAt: now.Add(-time.Minute)},
		{ThreadID: "existing", Name: "Other memory", CWD: child, Loaded: true, LastUsedAt: now.Add(-time.Hour)},
	}
	for id, root := range map[string]string{"inst-parent": parent, "inst-pool": pool} {
		svc.UpsertInstance(&state.InstanceRecord{
			InstanceID: id, WorkspaceRoot: root, WorkspaceKey: root,
			Source: "headless", Backend: agentproto.BackendCodex, Managed: true, Online: true,
			Threads: map[string]*state.ThreadRecord{
				"migrated": {ThreadID: "migrated", Name: "Memory", CWD: parent, WorkspaceKey: root},
				"existing": {ThreadID: "existing", Name: "Other memory", CWD: child, WorkspaceKey: root},
			},
		})
	}
	svc.SetPersistedThreadCatalog(&fakePersistedThreadCatalog{
		recent: rows, recentWorkspaces: map[string]time.Time{child: now.Add(-time.Minute)},
		byID: map[string]state.ThreadRecord{"migrated": rows[0], "existing": rows[1]},
	})
	for _, id := range []string{"parent", "memory"} {
		svc.MaterializeSurfaceResume(id, id, id, "user", state.ProductModeNormal, agentproto.BackendCodex, "", "", "")
	}
	owner := svc.root.Surfaces["parent"]
	owner.AttachedInstanceID = "inst-parent"
	owner.ClaimedWorkspaceKey = parent
	svc.bindInstanceClaim(owner, owner.AttachedInstanceID)
	surface := svc.root.Surfaces["memory"]
	options := svc.targetPickerSessionOptions(surface, workspaceSelectionEntry{workspaceKey: child}, control.TargetPickerRequestSourceList, true)
	for _, row := range rows {
		if !targetPickerHasSessionOption(options, targetPickerThreadValue(row.ThreadID)) {
			t.Errorf("saved session %s must be listed without a child runtime, got %#v", row.ThreadID, options)
		}
		view := svc.mergedThreadView(surface, row.ThreadID)
		if view == nil || !testutil.SamePath(mergedThreadWorkspaceClaimKey(view), child) {
			t.Errorf("exact lookup must use the same saved child workspace: %#v", view)
			continue
		}
		target := svc.resolveThreadTargetFromView(surface, view)
		if target.Mode == threadAttachUnavailable || target.NoticeCode == "workspace_busy" {
			t.Errorf("child session must be recoverable without claiming parent: %#v", target)
		}
	}
	if surface.AttachedInstanceID != "" || surface.ClaimedWorkspaceKey != "" || surface.SelectedThreadID != "" {
		t.Fatal("listing must not attach or claim the detached bot")
	}
	if owner.AttachedInstanceID != "inst-parent" || !testutil.SamePath(owner.ClaimedWorkspaceKey, parent) {
		t.Fatal("listing must not change the parent bot's attachment")
	}
}
