package runtime

// 本文件实现 public Runtime SDK，对 internal/runtime 做薄封装。
// 调用方：外部 Go 程序和本包测试；所有 internal 类型都被转换为 SDK DTO。

import (
	"context"
	"sync"
	"time"

	intruntime "github.com/Ozqi/walle/internal/runtime"
	"github.com/Ozqi/walle/internal/toolevent"
)

// Runtime 是可嵌入外部 Go 程序的 walle Runtime。
type Runtime struct {
	mu     sync.Mutex
	inner  *intruntime.Runtime
	busy   bool
	closed bool
}

// New 创建一个 SDK Runtime。
func New(ctx context.Context, opts Options) (*Runtime, error) {
	inner, err := intruntime.New(ctx, intruntime.Options{
		Debug: opts.Debug, SessionID: opts.SessionID, ContinueLast: opts.ContinueLast,
		ProjectDir: opts.ProjectDir, LLMFormat: opts.LLMFormat, LLMModel: opts.LLMModel,
		ModelRef: opts.ModelRef, PromptBase: opts.PromptBase,
	})
	if err != nil {
		return nil, err
	}
	return &Runtime{inner: inner}, nil
}

// Info 返回当前 Runtime 的只读状态。
func (r *Runtime) Info() Info {
	if r == nil {
		return Info{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inner == nil {
		return Info{}
	}
	return fromInternalInfo(r.inner.Info())
}

// Run 在当前 Runtime 的消息上下文中执行一轮 Agent。
func (r *Runtime) Run(ctx context.Context, input string, opts ...RunOption) (RunResult, error) {
	if r == nil {
		return RunResult{}, ErrClosed
	}
	started := time.Now().UTC()
	config := runConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&config)
		}
	}

	r.mu.Lock()
	if r.closed || r.inner == nil {
		r.mu.Unlock()
		return RunResult{}, ErrClosed
	}
	if r.busy {
		r.mu.Unlock()
		return RunResult{}, ErrBusy
	}
	r.busy = true
	inner := r.inner
	r.mu.Unlock()

	emit := func(event Event) {
		if config.onEvent != nil {
			config.onEvent(event)
		}
	}
	emit(Event{Type: EventUser, Text: input})
	emit(Event{Type: EventState, Busy: true})

	response, err := inner.RunStream(ctx, input, intruntime.StreamCallbacks{
		OnToken: func(token string) {
			emit(Event{Type: EventAssistant, Text: token, Turn: inner.Info().Turn})
		},
		OnReasoning: func(token string) {
			emit(Event{Type: EventThinking, Text: token, Turn: inner.Info().Turn})
		},
		OnTool: func(event toolevent.ToolEvent) {
			emit(fromToolEvent(event, inner.Info().Turn))
		},
	})
	info := fromInternalInfo(inner.Info())
	if err != nil {
		message := "LLM error: " + err.Error()
		emit(Event{Type: EventError, Text: message, Error: message, Turn: info.Turn})
	} else {
		emit(Event{Type: EventDone, Turn: info.Turn})
	}
	emit(Event{Type: EventState, Turn: info.Turn})
	r.mu.Lock()
	r.busy = false
	r.mu.Unlock()
	return RunResult{Response: response, StartedAt: started, EndedAt: time.Now().UTC(), SessionID: info.SessionID, ModelRef: info.ModelRef}, err
}

// Close 关闭 Runtime 持有的进程级资源。
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	if r.busy {
		r.mu.Unlock()
		return ErrBusy
	}
	r.closed = true
	inner := r.inner
	r.inner = nil
	r.mu.Unlock()
	if inner == nil {
		return nil
	}
	return inner.Close()
}
