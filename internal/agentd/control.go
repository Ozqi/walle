package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

const supervisorSocket = "supervisor.sock"

// ProcessSnapshot 是跨进程查询使用的只读 AgentProcess 快照。
type ProcessSnapshot struct {
	ID            string       `json:"id"`
	Name          string       `json:"name,omitempty"`
	State         ProcessState `json:"state"`
	StartedAt     time.Time    `json:"started_at"`
	Workspace     string       `json:"workspace"`
	WorkLogPath   string       `json:"worklog_path,omitempty"`
	Model         string       `json:"model,omitempty"`
	SessionID     string       `json:"session_id,omitempty"`
	Turn          int          `json:"turn,omitempty"`
	PromptTokens  int          `json:"prompt_tokens,omitempty"`
	TotalTokens   int          `json:"total_tokens,omitempty"`
	ContextWindow int          `json:"context_window,omitempty"`
	Interactive   bool         `json:"interactive,omitempty"`
}

// ProcessEventType 是 daemon 向 attached TUI 推送的事件类型。
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

// ProcessEvent 是 daemon 向 attached TUI 推送的结构化事件。
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

// InteractiveProcess 是控制通道依赖的最小长驻 Agent 接口。
type InteractiveProcess interface {
	Snapshot() ProcessSnapshot
	Attach() ([]ProcessEvent, <-chan ProcessEvent, func())
	Submit(string) error
	Stop() error
}

// OpenRequest 表达一次 CLI 打开 workspace interactive Runtime 的用户意图。
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

// InteractiveRegistry 由 daemon 层实现，agentd 只通过接口打开或查找交互进程。
type InteractiveRegistry interface {
	OpenInteractive(context.Context, OpenRequest) (InteractiveProcess, error)
	ListInteractive() []ProcessSnapshot
	FindInteractive(string) InteractiveProcess
}

// controlMessage 是 Unix Socket 上的一帧 NDJSON；ID 只关联 input/stop 请求与对应结果。
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

// Processes 返回当前运行中进程的副本，不外泄进程表中的可变指针。
func (s *Agentd) Processes(workspace string) []ProcessSnapshot {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var processes []ProcessSnapshot
	for _, proc := range s.processes {
		if proc == nil || proc.State != ProcessRunning {
			continue
		}
		processes = append(processes, ProcessSnapshot{
			ID: proc.ID, Name: proc.Name, State: proc.State,
			StartedAt: proc.StartedAt, Workspace: workspace, WorkLogPath: proc.workLogPath(),
		})
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].StartedAt.Before(processes[j].StartedAt) })
	return processes
}

// ControlServer 向同一用户的 CLI 暴露 daemon 进程。
type ControlServer struct {
	listener net.Listener
	path     string
	once     sync.Once
}

// StartControlServer 启动按 open 请求创建 interactive Runtime 的 supervisor socket。
func StartControlServer(ctx context.Context, controlDir string, sys *Agentd, workspace string, registry InteractiveRegistry) (*ControlServer, error) {
	// 1. 清理旧版 socket，并探测固定 supervisor socket 是否已有活跃 daemon。
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		return nil, fmt.Errorf("create daemon control dir: %w", err)
	}
	oldSockets, _ := filepath.Glob(filepath.Join(controlDir, "daemon-*.sock"))
	for _, old := range oldSockets {
		_ = os.Remove(old)
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
	// 2. 创建仅当前用户可访问的 Unix Socket；ctx 取消后 serve 会关闭并删除它。
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen daemon control socket: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	server := &ControlServer{listener: listener, path: path}
	go server.serve(ctx, sys, workspace, registry)
	return server, nil
}

func (s *ControlServer) serve(ctx context.Context, sys *Agentd, workspace string, registry InteractiveRegistry) {
	go func() { <-ctx.Done(); _ = s.Close() }()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go handleControlConn(ctx, conn, sys, workspace, registry)
	}
}

// handleControlConn 处理 list 短连接、open 创建连接或 attach 长连接。
// attach 按 attached -> history events -> ready -> live events 的顺序建立双向通道。
func handleControlConn(ctx context.Context, conn net.Conn, sys *Agentd, workspace string, registry InteractiveRegistry) {
	defer conn.Close()
	dec, enc := json.NewDecoder(conn), json.NewEncoder(conn)
	var first controlMessage
	if err := dec.Decode(&first); err != nil {
		return
	}
	// 1. 首帧决定短连接 list、open 或长连接 attach，其他请求不会进入双向循环。
	if first.Type == "list" {
		processes := sys.Processes(workspace)
		if registry != nil {
			processes = append(processes, registry.ListInteractive()...)
		}
		_ = enc.Encode(controlMessage{Type: "list", Processes: processes})
		return
	}
	var target InteractiveProcess
	if first.Type == "open" {
		if registry == nil {
			_ = enc.Encode(controlMessage{Type: "error", Error: "open is not supported"})
			return
		}
		proc, err := registry.OpenInteractive(ctx, OpenRequest{
			Workspace: first.Workspace, Continue: first.Continue, SessionID: first.SessionID,
			ModelRef: first.ModelRef, LLMFormat: first.LLMFormat, LLMModel: first.LLMModel,
			Debug: first.Debug, PromptBase: first.PromptBase,
		})
		if err != nil {
			_ = enc.Encode(controlMessage{Type: "error", Error: err.Error()})
			return
		}
		target = proc
	} else if first.Type == "attach" {
		if registry != nil {
			target = registry.FindInteractive(first.ProcessID)
		}
	} else {
		_ = enc.Encode(controlMessage{Type: "error", Error: "expected list, open or attach"})
		return
	}
	if target == nil {
		_ = enc.Encode(controlMessage{Type: "error", Error: "interactive process not found"})
		return
	}
	// 2. 先发送快照和完整历史，再以 ready 标记实时流边界。
	history, events, detach := target.Attach()
	defer detach()
	snapshot := target.Snapshot()
	if err := enc.Encode(controlMessage{Type: "attached", Process: &snapshot}); err != nil {
		return
	}
	for i := range history {
		if err := enc.Encode(controlMessage{Type: "event", Event: &history[i]}); err != nil {
			return
		}
	}
	if err := enc.Encode(controlMessage{Type: "ready"}); err != nil {
		return
	}
	// 3. 独立 goroutine 解码客户端请求，主循环串行编码事件和请求响应。
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
			// detach 或连接 EOF 只解除当前订阅，不停止 daemon 中的 Agent。
			if !ok || request.Type == "detach" {
				return
			}
			if request.Type == "input" {
				response := controlMessage{Type: "input_result", ID: request.ID}
				if err := target.Submit(request.Text); err != nil {
					response.Error = err.Error()
				}
				if enc.Encode(response) != nil {
					return
				}
				continue
			}
			if request.Type == "stop" {
				response := controlMessage{Type: "stop_result", ID: request.ID}
				if err := target.Stop(); err != nil {
					response.Error = err.Error()
				}
				if enc.Encode(response) != nil {
					return
				}
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

// IsSupervisorUnavailable 判断错误是否来自 supervisor socket 尚未就绪。
func IsSupervisorUnavailable(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}

// ListProcesses 查询用户级唯一 supervisor 中的进程。
func ListProcesses(controlDir string) ([]ProcessSnapshot, error) {
	path := filepath.Join(controlDir, supervisorSocket)
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
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
	processes := response.Processes
	sort.Slice(processes, func(i, j int) bool { return processes[i].StartedAt.Before(processes[j].StartedAt) })
	return processes, nil
}

// ProcessClient 是 attached TUI 使用的双向 NDJSON 客户端。
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

// OpenProcess 请求 daemon 为当前 workspace 打开 interactive Runtime，并订阅其事件。
func OpenProcess(controlDir string, req OpenRequest) (*ProcessClient, error) {
	return attachWithMessage(controlDir, "open", controlMessage{
		Type: "open", Workspace: req.Workspace, Continue: req.Continue, SessionID: req.SessionID,
		ModelRef: req.ModelRef, LLMFormat: req.LLMFormat, LLMModel: req.LLMModel,
		Debug: req.Debug, PromptBase: req.PromptBase,
	}, 30*time.Second)
}

// AttachProcess 连接 target 对应的 daemon 并订阅交互 Agent 事件。
func AttachProcess(controlDir string, target string) (*ProcessClient, error) {
	return attachWithMessage(controlDir, target, controlMessage{Type: "attach", ProcessID: target}, 2*time.Second)
}

func attachWithMessage(controlDir string, label string, first controlMessage, deadline time.Duration) (*ProcessClient, error) {
	// 1. 建立连接并完成 attached 握手；握手阶段设置总期限，避免无响应 daemon 卡住。
	conn, err := net.DialTimeout("unix", filepath.Join(controlDir, supervisorSocket), time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", label, err)
	}
	client := &ProcessClient{conn: conn, enc: json.NewEncoder(conn), events: make(chan ProcessEvent, 256), pending: make(map[uint64]chan error), done: make(chan struct{})}
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
	// 2. 同步收齐 ready 之前的历史事件，随后清除 deadline 并启动实时读取协程。
	var replay []ProcessEvent
	for {
		if err := dec.Decode(&response); err != nil {
			conn.Close()
			return nil, err
		}
		if response.Type == "ready" {
			break
		}
		if response.Type == "event" && response.Event != nil {
			replay = append(replay, *response.Event)
		}
	}
	client.events = make(chan ProcessEvent, len(replay)+256)
	for _, event := range replay {
		client.events <- event
	}
	_ = conn.SetDeadline(time.Time{})
	go client.read(dec)
	return client, nil
}

// read 转发实时事件，并按响应 ID 唤醒等待中的 input/stop 请求。
func (c *ProcessClient) read(dec *json.Decoder) {
	// 连接结束时唤醒全部 pending 请求并关闭事件流，避免调用方永久等待。
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
		} else if message.Type == "input_result" || message.Type == "stop_result" {
			c.mu.Lock()
			result := c.pending[message.ID]
			delete(c.pending, message.ID)
			c.mu.Unlock()
			if result != nil {
				if message.Error != "" {
					result <- fmt.Errorf("%s", message.Error)
				} else {
					result <- nil
				}
				close(result)
			}
		} else if message.Type == "error" {
			select {
			case c.events <- ProcessEvent{Type: ProcessEventError, Error: message.Error}:
			case <-c.done:
				return
			}
		}
	}
}

// Snapshot 返回 attach 时的远端状态。
func (c *ProcessClient) Snapshot() ProcessSnapshot { return c.snapshot }

// Events 返回 replay 与实时事件流。
func (c *ProcessClient) Events() <-chan ProcessEvent { return c.events }

// Submit 向 daemon Agent 提交一轮用户输入。
func (c *ProcessClient) Submit(text string) error {
	return c.sendControl("input", text)
}

// Stop 请求 daemon Agent 停止当前运行。
func (c *ProcessClient) Stop() error {
	return c.sendControl("stop", "")
}

// sendControl 先注册递增 ID 对应的等待项，再写请求帧并等待同 ID 响应。
// 写入失败、超时或连接关闭都会清理 pending，避免泄漏等待者。
func (c *ProcessClient) sendControl(kind string, text string) error {
	// 编码器写入和 pending 注册共用一把锁，确保响应到达时一定能找到等待者。
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

// Close 只断开 attached TUI，不取消 daemon Agent。
func (c *ProcessClient) Close() error {
	c.once.Do(func() { close(c.done) })
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.enc.Encode(controlMessage{Type: "detach"})
	return c.conn.Close()
}
