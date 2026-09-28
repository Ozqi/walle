package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Ozqi/walle/internal/commands"
	agentrt "github.com/Ozqi/walle/internal/runtime"
	"github.com/Ozqi/walle/internal/toolevent"
)

// DaemonSession 串行执行用户输入，并缓存 daemon 生命周期内的结构化输出供重连回放。
// 事件历史只保存在内存中，daemon 退出后丢失。
type DaemonSession struct {
	mu        sync.Mutex
	ctx       context.Context
	runtime   *agentrt.Runtime
	id        string
	name      string
	started   time.Time
	busy      bool
	provider  string
	runCancel context.CancelFunc
	seq       uint64
	events    []ProcessEvent
	subs      map[chan ProcessEvent]struct{}
}

// NewDaemonSession 为一个 Runtime 创建长驻交互会话。
func NewDaemonSession(ctx context.Context, rt *agentrt.Runtime, idName ...string) *DaemonSession {
	provider, _, _ := strings.Cut(rt.ModelRef, "/")
	id := "interactive"
	name := "interactive"
	if len(idName) > 0 && strings.TrimSpace(idName[0]) != "" {
		id = idName[0]
	}
	if len(idName) > 1 && strings.TrimSpace(idName[1]) != "" {
		name = idName[1]
	}
	return &DaemonSession{ctx: ctx, runtime: rt, id: id, name: name, provider: provider, started: time.Now().UTC(), subs: make(map[chan ProcessEvent]struct{})}
}

// Snapshot 返回 control socket 使用的只读会话状态。
func (s *DaemonSession) Snapshot() ProcessSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := ProcessIdle
	if s.busy {
		state = ProcessRunning
	}
	promptTokens, totalTokens, contextWindow := s.runtime.Agent.TokenUsage()
	return ProcessSnapshot{
		ID: s.id, Name: s.name, State: state, StartedAt: s.started,
		Workspace: s.runtime.ProjectDir, Model: s.runtime.ModelRef, SessionID: s.runtime.SessionID,
		Turn: s.runtime.Agent.CurrentTurn(), PromptTokens: promptTokens, TotalTokens: totalTokens,
		ContextWindow: contextWindow,
	}
}

// Attach 原子返回历史事件并注册实时订阅者。
func (s *DaemonSession) Attach() ([]ProcessEvent, <-chan ProcessEvent, func()) {
	// 在同一临界区复制历史并注册订阅者，避免 history 与实时流之间出现事件缺口。
	s.mu.Lock()
	history := append([]ProcessEvent(nil), s.events...)
	ch := make(chan ProcessEvent, 256)
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return history, ch, func() {
		s.mu.Lock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
}

// Submit 处理 slash command，或在会话空闲时异步启动一轮 Agent。
// socket 断开只解除事件订阅，不会取消已经启动的执行。
func (s *DaemonSession) Submit(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("input is required")
	}
	if strings.HasPrefix(text, "/") {
		s.publish(ProcessEvent{Type: ProcessEventUser, Text: text})
		if text == "/stop" {
			return s.stop()
		}
		if s.handlePickerSlash(text) {
			return nil
		}
		s.publish(ProcessEvent{Type: ProcessEventSystem, Text: s.handleSlash(text)})
		return nil
	}
	// 普通输入只允许单轮执行；取消函数与 busy 在启动 goroutine 前一起发布。
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return fmt.Errorf("agent is busy")
	}
	runCtx, cancel := context.WithCancel(s.ctx)
	s.busy = true
	s.runCancel = cancel
	s.mu.Unlock()
	s.publish(ProcessEvent{Type: ProcessEventUser, Text: text})
	s.publish(ProcessEvent{Type: ProcessEventState, Busy: true})
	go s.run(runCtx, text)
	return nil
}

// Stop 停止当前 attached daemon 会话正在执行的一轮 Agent。
func (s *DaemonSession) Stop() error {
	return s.stop()
}

// Close 释放当前 Runtime 持有的进程级资源。
func (s *DaemonSession) Close() error {
	if s == nil || s.runtime == nil {
		return nil
	}
	return s.runtime.Close()
}

func (s *DaemonSession) stop() error {
	s.mu.Lock()
	cancel := s.runCancel
	if !s.busy || cancel == nil {
		s.mu.Unlock()
		s.publish(ProcessEvent{Type: ProcessEventSystem, Text: "no active run"})
		return nil
	}
	s.mu.Unlock()
	cancel()
	return nil
}

// handlePickerSlash 处理 /provider 和 /model 的 picker、登录和异步模型切换。
// 该路径只在 Agent 空闲时运行；真正的模型切换由 Runtime.SwitchModel 串行化。
func (s *DaemonSession) handlePickerSlash(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 || (fields[0] != "/provider" && fields[0] != "/model") {
		return false
	}
	s.mu.Lock()
	busy := s.busy
	s.mu.Unlock()
	if busy {
		s.publish(ProcessEvent{Type: ProcessEventSystem, Text: "agent is busy"})
		return true
	}
	if fields[0] == "/provider" {
		// provider 无参数时只返回候选列表；有参数时切换当前 provider 并按需触发登录。
		if len(fields) == 1 {
			providers := providers(s.runtime)
			options := make([]string, 0, len(providers))
			for _, provider := range providers {
				options = append(options, provider.Name)
			}
			s.publish(ProcessEvent{Type: ProcessEventPicker, Kind: "provider", Options: options})
			return true
		}
		if len(fields) != 2 {
			s.publish(ProcessEvent{Type: ProcessEventSystem, Text: "usage: /provider [name]"})
			return true
		}
		s.setProvider(fields[1])
		provider := s.currentProvider()
		if provider == "openai" {
			for _, provider := range providers(s.runtime) {
				if provider.Name == "openai" && provider.LoggedIn {
					go s.publishModels("openai")
					return true
				}
			}
			loginURL, done, err := startOpenAILoginFunc(s.ctx)
			if err != nil {
				s.publish(ProcessEvent{Type: ProcessEventSystem, Text: err.Error()})
				return true
			}
			s.publish(ProcessEvent{Type: ProcessEventSystem, Text: "Open this URL to sign in with ChatGPT:\n" + loginURL})
			go func() {
				if err := <-done; err != nil {
					s.publish(ProcessEvent{Type: ProcessEventSystem, Text: "Codex login failed: " + err.Error()})
					return
				}
				s.publish(ProcessEvent{Type: ProcessEventSystem, Text: "Codex login complete"})
				go s.publishModels("openai")
			}()
			return true
		}
		go s.publishModels(provider)
		return true
	}
	// model 无参数时返回当前 provider 的模型列表；有参数时异步切换目标模型。
	if len(fields) == 1 {
		go s.publishModels(s.currentProvider())
		return true
	}
	if len(fields) != 2 {
		s.publish(ProcessEvent{Type: ProcessEventSystem, Text: "usage: /model [name]"})
		return true
	}
	modelRef := fields[1]
	if !strings.Contains(modelRef, "/") {
		modelRef = s.currentProvider() + "/" + modelRef
	}
	go s.switchModel(modelRef)
	return true
}

func (s *DaemonSession) switchModel(modelRef string) {
	// handlePickerSlash 已在启动 goroutine 前检查 busy；SwitchModel 自身只串行化多个切换请求。
	result, err := s.runtime.SwitchModel(s.ctx, modelRef)
	if err != nil {
		s.publish(ProcessEvent{Type: ProcessEventSystem, Text: err.Error()})
		return
	}
	provider, _, _ := strings.Cut(result, "/")
	s.setProvider(provider)
	s.publish(ProcessEvent{Type: ProcessEventModel, Text: result})
	s.publish(ProcessEvent{Type: ProcessEventSystem, Text: "Switched model: " + result})
}

func (s *DaemonSession) currentProvider() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.provider
}

func (s *DaemonSession) setProvider(provider string) {
	s.mu.Lock()
	s.provider = provider
	s.mu.Unlock()
}

func (s *DaemonSession) publishModels(provider string) {
	models, err := providerModelsFunc(s.ctx, provider)
	if err != nil {
		s.publish(ProcessEvent{Type: ProcessEventSystem, Text: err.Error()})
		return
	}
	s.publish(ProcessEvent{Type: ProcessEventPicker, Kind: "model", Name: provider, Options: models})
}

func (s *DaemonSession) handleSlash(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	var result string
	var err error
	switch fields[0] {
	case "/skill":
		result, err = commands.HandleSkill(text, s.runtime.Agent.GetSkillManager())
	case "/mcp":
		result, err = commands.HandleMCP(text)
	case "/compress":
		result, err = commands.HandleCompress(s.ctx, text, s.runtime.CtxManager, s.runtime.MessageCtx, s.runtime.Agent.GetModel(), s.runtime.PromptDir, "compact")
	case "/stop":
		if err := s.stop(); err != nil {
			return err.Error()
		}
		return ""
	case "/session":
		return fmt.Sprintf("Current session: %s", s.runtime.SessionID)
	default:
		return "unknown slash command"
	}
	if err != nil {
		return err.Error()
	}
	return result
}

func (s *DaemonSession) run(runCtx context.Context, text string) {
	// 1. 当前轮临时接管 Agent 工具事件 sink，并把 token/tool 事件转成 daemon 事件。
	prev := s.runtime.Agent.SetToolEventSink(func(event toolevent.ToolEvent) {
		s.runtime.RecordToolEvent(event)
		s.publish(ProcessEvent{
			Type: ProcessEventTool, Kind: event.Kind, Name: event.Name, Args: event.Args,
			Text: event.Text, Result: event.Result, Error: event.Error, Concurrent: event.Concurrent,
		})
	})
	defer s.runtime.Agent.SetToolEventSink(prev)
	_, err := s.runtime.Agent.RunStream(runCtx, s.runtime.MessageCtx, text,
		func(token string) { s.publish(ProcessEvent{Type: ProcessEventAssistant, Text: token}) },
		func(token string) { s.publish(ProcessEvent{Type: ProcessEventThinking, Text: token}) },
	)
	// 2. 在锁内先清理运行状态再发布终态 state，避免 TUI 收到过期 busy。
	s.mu.Lock()
	s.busy = false
	s.runCancel = nil
	if runCtx.Err() != nil {
		s.publishLocked(ProcessEvent{Type: ProcessEventSystem, Text: "stopped current run"})
	} else if err != nil {
		message := "LLM error: " + err.Error()
		s.publishLocked(ProcessEvent{Type: ProcessEventError, Text: message, Error: message})
	} else {
		s.publishLocked(ProcessEvent{Type: ProcessEventDone})
	}
	s.publishLocked(ProcessEvent{Type: ProcessEventState})
	s.mu.Unlock()
}

func (s *DaemonSession) publish(event ProcessEvent) {
	s.mu.Lock()
	s.publishLocked(event)
	s.mu.Unlock()
}

func (s *DaemonSession) publishLocked(event ProcessEvent) {
	if s.runtime != nil && s.runtime.Agent != nil {
		if event.Turn == 0 {
			event.Turn = s.runtime.Agent.CurrentTurn()
		}
		if event.ContextWindow == 0 {
			event.PromptTokens, event.TotalTokens, event.ContextWindow = s.runtime.Agent.TokenUsage()
		}
	}
	// 调用方必须持有 s.mu，保证 seq、历史和订阅者集合在同一临界区更新。
	// 连续 token 在历史中合并以限制重放体积，实时订阅仍收到原始增量事件。
	s.seq++
	event.Seq = s.seq
	if len(s.events) > 0 && (event.Type == "assistant" || event.Type == "thinking") && s.events[len(s.events)-1].Type == event.Type {
		s.events[len(s.events)-1].Text += event.Text
		s.events[len(s.events)-1].Seq = event.Seq
	} else {
		s.events = append(s.events, event)
	}
	for ch := range s.subs {
		// 慢订阅者不会阻塞 Agent；缓冲区满时直接断开，由客户端重新 attach 获取历史。
		select {
		case ch <- event:
		default:
			delete(s.subs, ch)
			close(ch)
		}
	}
}
