package orchestrator

import (
	"strings"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func (s *Service) mergePersistedRecentThreadsForBackend(viewsByID map[string]*mergedThreadView, backend agentproto.Backend, filterByBackend bool) {
	if s == nil || !s.catalog.hasPersistedThreads() {
		return
	}
	threads := s.catalog.recentPersistedThreads(persistedRecentThreadLimit)
	if filterByBackend {
		threads = s.catalog.recentPersistedThreadsForBackend(backend, persistedRecentThreadLimit)
	}
	catalogBackend := agentproto.BackendCodex
	if filterByBackend {
		catalogBackend = backend
	}
	workspaces := s.catalog.recentPersistedWorkspacesForBackend(catalogBackend, persistedRecentWorkspaceLimit)
	for i := range threads {
		thread := threads[i]
		if strings.TrimSpace(thread.ThreadID) == "" || !ordinaryThreadVisible(&thread) {
			continue
		}
		view := viewsByID[thread.ThreadID]
		if view == nil {
			viewsByID[thread.ThreadID] = &mergedThreadView{
				ThreadID: thread.ThreadID,
				Backend:  backend,
				Inst:     syntheticPersistedThreadInstance(&thread, backend),
				Thread:   cloneThreadRecord(&thread),
			}
			continue
		}
		view.Thread = s.mergePersistedThreadMetadata(view.Thread, &thread, view.Backend, workspaces)
	}
	// Native loaded lists may contain older threads outside the global recent
	// window. Refresh their names by identity without changing workspace facts.
	ids := make([]string, 0, len(viewsByID))
	for id, view := range viewsByID {
		if agentproto.NormalizeBackend(view.Backend) == agentproto.BackendCodex {
			ids = append(ids, id)
		}
	}
	names := s.catalog.explicitThreadNames(agentproto.BackendCodex, ids)
	for _, view := range viewsByID {
		if agentproto.NormalizeBackend(view.Backend) == agentproto.BackendCodex {
			view.Thread = threadWithPersistedName(view.Thread, names)
		}
	}
}

// A native thread/list is global and snapshots may label every returned thread
// with the reporting instance's workspace. After a restart those copies have
// no LastUsedAt, so their winning label can depend on an arbitrary instance ID.
// When the persisted record supplies the newer cwd, resolve it against known
// workspace roots instead of treating that copied label as authoritative.
func (s *Service) mergePersistedThreadMetadata(current, persisted *state.ThreadRecord, backend agentproto.Backend, workspaces map[string]time.Time) *state.ThreadRecord {
	merged := mergeThreadMetadata(current, persisted)
	if merged != nil && persisted != nil && agentproto.NormalizeBackend(backend) == agentproto.BackendCodex {
		// A saved explicit name is independent of activity recency. Keep the
		// existing recency rules for cwd, workspace, lifecycle and other facts.
		if name := strings.TrimSpace(persisted.PersistedName); name != "" {
			merged.Name = name
		}
	}
	if merged == nil || persisted == nil || strings.TrimSpace(persisted.CWD) == "" ||
		strings.TrimSpace(persisted.WorkspaceKey) != "" || !shouldPromoteMergedThreadMetadata(current, persisted) {
		return merged
	}
	cwd := normalizeWorkspaceClaimKey(persisted.CWD)
	workspaceKey := ""
	consider := func(key string) {
		key = normalizeWorkspaceClaimKey(key)
		if key == "" || (cwd != key && !strings.HasPrefix(cwd, key+"/")) {
			return
		}
		if len(key) > len(workspaceKey) {
			workspaceKey = key
		}
	}
	// Preserve a stable root for an ordinary cwd change within that workspace.
	consider(merged.WorkspaceKey)
	// The picker already offers saved workspaces before their runtime exists.
	// Use the same roots for thread grouping and exact-thread recovery.
	for key := range workspaces {
		consider(key)
	}
	for _, inst := range s.root.Instances {
		if inst != nil && state.EffectiveInstanceBackend(inst) == backend {
			consider(instanceWorkspaceClaimKey(inst))
		}
	}
	for _, surface := range s.root.Surfaces {
		if surface != nil && s.surfaceUsesWorkspaceClaims(surface) && s.surfaceBackend(surface) == backend {
			consider(s.surfaceCurrentWorkspaceKey(surface))
		}
	}
	if workspaceKey == "" {
		workspaceKey = cwd
	}
	merged.WorkspaceKey = workspaceKey
	return merged
}

func (s *Service) normalModeThreadBackend(surface *state.SurfaceConsoleRecord) (agentproto.Backend, bool) {
	if !s.surfaceIsHeadless(surface) {
		return "", false
	}
	return s.surfaceBackend(surface), true
}
