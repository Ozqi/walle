// Package daemon 提供用户级 daemon 的 Unix Socket 控制协议。
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const supervisorSocket = "supervisor.sock"

// ProcessState 表示交互 Runtime 是否正在执行。
type ProcessState string

const (
	ProcessIdle    ProcessState = "idle"
	ProcessRunning ProcessState = "running"
)

// ProcessSnapshot 是 daemon 进程列表和 attach 握手使用的只读快照。
type ProcessSnapshot struct {
	ID            string       `json:"id"`
	Name          string       `json:"name,omitempty"`
	State         ProcessState `json:"state"`
	StartedAt     time.Time    `json:"started_at"`
	Workspace     string       `json:"workspace"`
	Model         string       `json:"model,omitempty"`
	SessionID     string       `json:"session_id,omitempty"`
	Turn          int          `json:"turn,omitempty"`
	PromptTokens  int          `json:"prompt_tokens,omitempty"`
	TotalTokens   int          `json:"total_tokens,omitempty"`
	ContextWindow int          `json:"context_window,omitempty"`
}

// ProcessEventType 是 daemon 向 TUI 推送的事件类型。
type ProcessEventType string

const (
	ProcessEventUser      ProcessEventType = "user"
	ProcessEventAssistant ProcessEventType = "assistant"
	ProcessEventThinking  ProcessEventType = "thinking"
	ProcessEventTool      ProcessEventType = "tool"
	ProcessEventSystem    ProcessEventType = "system"
	ProcessEventError     ProcessEventType = "error"
	ProcessEventDone      ProcessEventType = "done"
	ProcessEventState     ProcessEventType = "state"
	ProcessEventPicker    ProcessEventType = "picker"
	ProcessEventModel     ProcessEventType = "model"
)

// ProcessEvent 是 daemon 向 TUI 推送的结构化事件。
type ProcessEvent struct {
	Seq           uint64           `json:"seq"`
	Type          ProcessEventType `json:"type"`
	Text          string           `json:"text,omitempty"`
	Kind          string           `json:"kind,omitempty"`
	Name          string           `json:"name,omitempty"`
	Args          string           `json:"args,omitempty"`
	Result        string           `json:"result,omitempty"`
	Error         string           `json:"error,omitempty"`
	Busy          bool             `json:"busy,omitempty"`
	Turn          int              `json:"turn,omitempty"`
	PromptTokens  int              `json:"prompt_tokens,omitempty"`
	TotalTokens   int              `json:"total_tokens,omitempty"`
	ContextWindow int              `json:"context_window,omitempty"`
	Options       []string         `json:"options,omitempty"`
	Concurrent    bool             `json:"concurrent,omitempty"`
}

// OpenRequest 表达一次打开 workspace Runtime 的用户意图。
type OpenRequest struct {
	Workspace  string `json:"workspace,omitempty"`
	Continue   bool   `json:"continue,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	ModelRef   string `json:"model_ref,omitempty"`
	LLMFormat  string `json:"llm_format,omitempty"`
	LLMModel   string `json:"llm_model,omitempty"`
	Debug      bool   `json:"debug,omitempty"`
	PromptBase string `json:"prompt_base,omitempty"`
}

type controlMessage struct {
	Type       string            `json:"type"`
	ID         uint64            `json:"id,omitempty"`
	ProcessID  string            `json:"process_id,omitempty"`
	Text       string            `json:"text,omitempty"`
	Workspace  string            `json:"workspace,omitempty"`
	Continue   bool              `json:"continue,omitempty"`
	SessionID  string            `json:"session_id,omitempty"`
	ModelRef   string            `json:"model_ref,omitempty"`
	LLMFormat  string            `json:"llm_format,omitempty"`
	LLMModel   string            `json:"llm_model,omitempty"`
	Debug      bool              `json:"debug,omitempty"`
	PromptBase string            `json:"prompt_base,omitempty"`
	Processes  []ProcessSnapshot `json:"processes,omitempty"`
	Process    *ProcessSnapshot  `json:"process,omitempty"`
	Event      *ProcessEvent     `json:"event,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// ControlServer 是用户级 daemon 的 Unix Socket 服务。
type ControlServer struct {
	listener net.Listener
	path     string
	once     sync.Once
}

// StartControlServer 启动 daemon 控制 socket。
func StartControlServer(ctx context.Context, controlDir string, registry *Registry) (*ControlServer, error) {
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		return nil, fmt.Errorf("create daemon control dir: %w", err)
	}
	path := filepath.Join(controlDir, supervisorSocket)
	if _, err := os.Stat(path); err == nil {
		conn, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return nil, fmt.Errorf("supervisor already running at %s", path)
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("probe supervisor socket %s: %w", path, dialErr)
		}
		_ = os.Remove(path)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat supervisor socket %s: %w", path, err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen daemon control socket: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	server := &ControlServer{listener: listener, path: path}
	go server.serve(ctx, registry)
	return server, nil
}

func (s *ControlServer) serve(ctx context.Context, registry *Registry) {
	go func() { <-ctx.Done(); _ = s.Close() }()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go handleControlConn(ctx, conn, registry)
	}
}

func handleControlConn(ctx context.Context, conn net.Conn, registry *Registry) {
	defer conn.Close()
	dec, enc := json.NewDecoder(conn), json.NewEncoder(conn)
	var first controlMessage
	if dec.Decode(&first) != nil {
		return
	}
	if first.Type == "list" {
		_ = enc.Encode(controlMessage{Type: "list", Processes: registry.list()})
		return
	}
	var target *DaemonSession
	var err error
	switch first.Type {
	case "open":
		target, err = registry.open(ctx, OpenRequest{
			Workspace: first.Workspace, Continue: first.Continue, SessionID: first.SessionID,
			ModelRef: first.ModelRef, LLMFormat: first.LLMFormat, LLMModel: first.LLMModel,
			Debug: first.Debug, PromptBase: first.PromptBase,
		})
	case "attach":
		target = registry.find(first.ProcessID)
		if target == nil {
			err = fmt.Errorf("interactive process not found")
		}
	default:
		err = fmt.Errorf("expected list, open or attach")
	}
	if err != nil {
		_ = enc.Encode(controlMessage{Type: "error", Error: err.Error()})
		return
	}
	history, events, detach := target.Attach()
	defer detach()
	snapshot := target.Snapshot()
	if enc.Encode(controlMessage{Type: "attached", Process: &snapshot}) != nil {
		return
	}
	for i := range history {
		if enc.Encode(controlMessage{Type: "event", Event: &history[i]}) != nil {
			return
		}
	}
	if enc.Encode(controlMessage{Type: "ready"}) != nil {
		return
	}
	requests := make(chan controlMessage)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(requests)
		for {
			var request controlMessage
			if dec.Decode(&request) != nil {
				return
			}
			select {
			case requests <- request:
			case <-done:
				return
			}
		}
	}()
	for {
		select {
		case event, ok := <-events:
			if !ok || enc.Encode(controlMessage{Type: "event", Event: &event}) != nil {
				return
			}
		case request, ok := <-requests:
			if !ok || request.Type == "detach" {
				return
			}
			response := controlMessage{ID: request.ID}
			switch request.Type {
			case "input":
				response.Type = "input_result"
				if err := target.Submit(request.Text); err != nil {
					response.Error = err.Error()
				}
			case "stop":
				response.Type = "stop_result"
				if err := target.Stop(); err != nil {
					response.Error = err.Error()
				}
			default:
				continue
			}
			if enc.Encode(response) != nil {
				return
			}
		}
	}
}

// Close 关闭控制 socket 并删除 socket 文件。
func (s *ControlServer) Close() error {
	if s == nil {
		return nil
	}
	var err error
	s.once.Do(func() {
		err = s.listener.Close()
		_ = os.Remove(s.path)
	})
	return err
}

// IsSupervisorUnavailable 判断 daemon socket 是否尚未就绪。
func IsSupervisorUnavailable(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}

// ListProcesses 查询 daemon 中的 Runtime。
func ListProcesses(controlDir string) ([]ProcessSnapshot, error) {
	conn, err := net.DialTimeout("unix", filepath.Join(controlDir, supervisorSocket), 200*time.Millisecond)
	if err != nil {
		return nil, nil
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if err := json.NewEncoder(conn).Encode(controlMessage{Type: "list"}); err != nil {
		return nil, err
	}
	var response controlMessage
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return nil, err
	}
	if response.Type != "list" {
		return nil, fmt.Errorf("unexpected supervisor response %q", response.Type)
	}
	return response.Processes, nil
}

// ProcessClient 是 TUI 使用的双向 daemon 客户端。
type ProcessClient struct {
	conn     net.Conn
	enc      *json.Encoder
	mu       sync.Mutex
	once     sync.Once
	done     chan struct{}
	nextID   uint64
	pending  map[uint64]chan error
	snapshot ProcessSnapshot
	events   chan ProcessEvent
}

// OpenProcess 创建 Runtime 并订阅事件。
func OpenProcess(controlDir string, req OpenRequest) (*ProcessClient, error) {
	return attachWithMessage(controlDir, "open", controlMessage{
		Type: "open", Workspace: req.Workspace, Continue: req.Continue, SessionID: req.SessionID,
		ModelRef: req.ModelRef, LLMFormat: req.LLMFormat, LLMModel: req.LLMModel,
		Debug: req.Debug, PromptBase: req.PromptBase,
	}, 30*time.Second)
}

// AttachProcess 连接已存在的 Runtime。
func AttachProcess(controlDir string, target string) (*ProcessClient, error) {
	return attachWithMessage(controlDir, target, controlMessage{Type: "attach", ProcessID: target}, 2*time.Second)
}

func attachWithMessage(controlDir string, label string, first controlMessage, deadline time.Duration) (*ProcessClient, error) {
	conn, err := net.DialTimeout("unix", filepath.Join(controlDir, supervisorSocket), time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", label, err)
	}
	client := &ProcessClient{conn: conn, enc: json.NewEncoder(conn), pending: make(map[uint64]chan error), done: make(chan struct{})}
	_ = conn.SetDeadline(time.Now().Add(deadline))
	if err := client.enc.Encode(first); err != nil {
		conn.Close()
		return nil, err
	}
	dec := json.NewDecoder(conn)
	var response controlMessage
	if err := dec.Decode(&response); err != nil {
		conn.Close()
		return nil, err
	}
	if response.Type != "attached" || response.Process == nil {
		conn.Close()
		return nil, fmt.Errorf("%s: %s", label, response.Error)
	}
	client.snapshot = *response.Process
	var replay []ProcessEvent
	for {
		if err := dec.Decode(&response); err != nil {
			conn.Close()
			return nil, err
		}
		if response.Type == "ready" {
			break
		}
		if response.Type == "event" && response.Event != nil && shouldReplayEvent(*response.Event) {
			replay = append(replay, *response.Event)
		}
	}
	replay = append(replay, snapshotStateEvent(client.snapshot))
	client.events = make(chan ProcessEvent, len(replay)+256)
	for _, event := range replay {
		client.events <- event
	}
	_ = conn.SetDeadline(time.Time{})
	go client.read(dec)
	return client, nil
}

func shouldReplayEvent(event ProcessEvent) bool {
	// picker 是 attached TUI 的临时模态状态；重连只回放事实事件，避免旧 picker 抢占当前输入。
	return event.Type != ProcessEventPicker
}

func snapshotStateEvent(snapshot ProcessSnapshot) ProcessEvent {
	return ProcessEvent{
		Type:          ProcessEventState,
		Busy:          snapshot.State == ProcessRunning,
		Turn:          snapshot.Turn,
		PromptTokens:  snapshot.PromptTokens,
		TotalTokens:   snapshot.TotalTokens,
		ContextWindow: snapshot.ContextWindow,
	}
}

func (c *ProcessClient) read(dec *json.Decoder) {
	defer func() {
		c.mu.Lock()
		for id, result := range c.pending {
			delete(c.pending, id)
			result <- fmt.Errorf("daemon connection closed")
			close(result)
		}
		c.mu.Unlock()
		close(c.events)
	}()
	for {
		var message controlMessage
		if dec.Decode(&message) != nil {
			return
		}
		if message.Type == "event" && message.Event != nil {
			select {
			case c.events <- *message.Event:
			case <-c.done:
				return
			}
			continue
		}
		if message.Type != "input_result" && message.Type != "stop_result" {
			continue
		}
		c.mu.Lock()
		result := c.pending[message.ID]
		delete(c.pending, message.ID)
		c.mu.Unlock()
		if result != nil {
			if message.Error != "" {
				result <- errors.New(message.Error)
			} else {
				result <- nil
			}
			close(result)
		}
	}
}

func (c *ProcessClient) Snapshot() ProcessSnapshot   { return c.snapshot }
func (c *ProcessClient) Events() <-chan ProcessEvent { return c.events }
func (c *ProcessClient) Submit(text string) error    { return c.sendControl("input", text) }
func (c *ProcessClient) Stop() error                 { return c.sendControl("stop", "") }
func (c *ProcessClient) sendControl(kind, text string) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	result := make(chan error, 1)
	c.pending[id] = result
	_ = c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	err := c.enc.Encode(controlMessage{Type: kind, ID: id, Text: text})
	_ = c.conn.SetWriteDeadline(time.Time{})
	if err != nil {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%s timed out", kind)
	}
}

// Close 只断开 TUI，不停止 daemon 中的 Runtime。
func (c *ProcessClient) Close() error {
	c.once.Do(func() { close(c.done) })
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.enc.Encode(controlMessage{Type: "detach"})
	return c.conn.Close()
}
