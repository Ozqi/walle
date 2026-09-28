package runtime

import (
	"errors"
	"time"

	intruntime "github.com/Ozqi/walle/internal/runtime"
	"github.com/Ozqi/walle/internal/toolevent"
)

var (
	ErrBusy   = errors.New("runtime is busy")
	ErrClosed = errors.New("runtime is closed")
)

// Options 控制 Runtime 初始化。
type Options struct {
	Debug        bool
	SessionID    string
	ContinueLast bool
	ProjectDir   string
	LLMFormat    string
	LLMModel     string
	ModelRef     string
	PromptBase   string
}

// Info 是 Runtime 的只读状态。
type Info struct {
	SessionID  string `json:"session_id,omitempty"`
	PromptBase string `json:"prompt_base,omitempty"`
	ModelRef   string `json:"model_ref,omitempty"`
	ProjectDir string `json:"project_dir,omitempty"`
	Turn       int    `json:"turn,omitempty"`
}

// EventType 是执行事件类型。
type EventType string

const (
	EventUser      EventType = "user"
	EventAssistant EventType = "assistant"
	EventThinking  EventType = "thinking"
	EventTool      EventType = "tool"
	EventError     EventType = "error"
	EventDone      EventType = "done"
	EventState     EventType = "state"
)

// Event 是单轮执行事件。
type Event struct {
	Type       EventType `json:"type"`
	Text       string    `json:"text,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Name       string    `json:"name,omitempty"`
	Args       string    `json:"args,omitempty"`
	Result     string    `json:"result,omitempty"`
	Error      string    `json:"error,omitempty"`
	Busy       bool      `json:"busy,omitempty"`
	Turn       int       `json:"turn,omitempty"`
	Concurrent bool      `json:"concurrent,omitempty"`
}

// RunResult 是 Runtime.Run 的最终结果。
type RunResult struct {
	Response  string    `json:"response"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	SessionID string    `json:"session_id,omitempty"`
	ModelRef  string    `json:"model_ref,omitempty"`
}

type runConfig struct{ onEvent func(Event) }

// RunOption 调整单次执行。
type RunOption func(*runConfig)

// WithEventHandler 注册流式事件回调。
func WithEventHandler(handler func(Event)) RunOption {
	return func(config *runConfig) { config.onEvent = handler }
}

func fromInternalInfo(info intruntime.Info) Info {
	return Info{SessionID: info.SessionID, PromptBase: info.PromptBase, ModelRef: info.ModelRef, ProjectDir: info.ProjectDir, Turn: info.Turn}
}

func fromToolEvent(event toolevent.ToolEvent, turn int) Event {
	return Event{Type: EventTool, Kind: event.Kind, Name: event.Name, Args: event.Args, Text: event.Text, Result: event.Result, Error: event.Error, Concurrent: event.Concurrent, Turn: turn}
}
