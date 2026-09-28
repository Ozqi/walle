package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	agentrt "github.com/Ozqi/walle/internal/runtime"
)

// Registry 托管 daemon 中的 workspace Runtime。
type Registry struct {
	ctx      context.Context
	mu       sync.Mutex
	nextID   int
	sessions map[string]*DaemonSession
}

// NewRegistry 创建空 Runtime 注册表。
func NewRegistry(ctx context.Context) *Registry {
	return &Registry{ctx: ctx, sessions: make(map[string]*DaemonSession)}
}

func (r *Registry) open(ctx context.Context, req OpenRequest) (*DaemonSession, error) {
	workspace := strings.TrimSpace(req.Workspace)
	if workspace == "" {
		return nil, fmt.Errorf("workspace is required")
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	r.mu.Lock()
	if req.SessionID != "" {
		if session := r.findLocked(workspace, req.SessionID, false); session != nil {
			r.mu.Unlock()
			return session, nil
		}
	} else if req.Continue {
		if session := r.findLocked(workspace, "", true); session != nil {
			r.mu.Unlock()
			return session, nil
		}
	}
	r.nextID++
	id := fmt.Sprintf("interactive-%d", r.nextID)
	r.mu.Unlock()
	promptBase := req.PromptBase
	if promptBase == "" {
		promptBase = "tui"
	}
	runtime, err := agentrt.New(ctx, agentrt.Options{
		Debug: req.Debug, SessionID: req.SessionID, ContinueLast: req.Continue && req.SessionID == "",
		ProjectDir: workspace, LLMFormat: req.LLMFormat, LLMModel: req.LLMModel,
		ModelRef: req.ModelRef, PromptBase: promptBase,
	})
	if err != nil {
		return nil, err
	}
	session := NewDaemonSession(r.ctx, runtime, id, filepath.Base(workspace))
	r.mu.Lock()
	r.sessions[id] = session
	r.mu.Unlock()
	return session, nil
}

func (r *Registry) list() []ProcessSnapshot {
	r.mu.Lock()
	processes := make([]ProcessSnapshot, 0, len(r.sessions))
	for _, session := range r.sessions {
		processes = append(processes, session.Snapshot())
	}
	r.mu.Unlock()
	sort.Slice(processes, func(i, j int) bool { return processes[i].StartedAt.Before(processes[j].StartedAt) })
	return processes
}

func (r *Registry) find(id string) *DaemonSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[id]
}

func (r *Registry) findLocked(workspace, sessionID string, idleOnly bool) *DaemonSession {
	var latest *DaemonSession
	var latestSnapshot ProcessSnapshot
	for _, session := range r.sessions {
		snapshot := session.Snapshot()
		if snapshot.Workspace != workspace || sessionID != "" && snapshot.SessionID != sessionID || idleOnly && snapshot.State != ProcessIdle {
			continue
		}
		if latest == nil || snapshot.StartedAt.After(latestSnapshot.StartedAt) {
			latest, latestSnapshot = session, snapshot
		}
	}
	return latest
}

// Close 关闭所有 Runtime。
func (r *Registry) Close() {
	r.mu.Lock()
	sessions := make([]*DaemonSession, 0, len(r.sessions))
	for _, session := range r.sessions {
		sessions = append(sessions, session)
	}
	r.mu.Unlock()
	for _, session := range sessions {
		_ = session.Close()
	}
}
