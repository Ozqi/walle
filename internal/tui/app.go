// Package tui 实现基于 Bubble Tea 的终端对话界面、daemon attach 界面和运行状态渲染。
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Ozqi/walle/internal/agentd"
	"github.com/Ozqi/walle/internal/toolevent"
	"github.com/Ozqi/walle/internal/tools"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	roleUser         = "user"
	roleAssistant    = "assistant"
	roleSystem       = "system"
	roleIntro        = "intro"
	roleHint         = "hint"
	roleThinking     = "thinking"
	defaultTUIWidth  = 100
	defaultTUIHeight = 30
	quitConfirmDelay = 2 * time.Second
	maxHintRows      = 12
)

type conversationEntry struct {
	Role        string
	Content     string
	CreatedAt   string
	ToolName    string
	ToolIntent  string
	ToolArgs    string
	ToolKey     string
	ToolState   string
	ToolOutput  string
	SystemTitle string

	renderCacheKey   string
	renderCacheWidth int
	renderCacheFrame int
	renderCacheText  string
}

func entryNow(entry conversationEntry) conversationEntry {
	if entry.CreatedAt == "" {
		entry.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return entry
}

type statusSnapshot struct {
	Runtime runtimeMeta
}

type runtimeMeta struct {
	Busy           bool
	State          string
	Turn           int
	ScrollPercent  int
	ToolCallsTotal int
	LastToolName   string
	PendingInput   bool
	SessionID      string
	Workdir        string
	Git            gitMeta
}

type gitMeta struct {
	Repo      bool
	Worktree  bool
	Branch    string
	Dirty     bool
	Shortstat string
}

// AppModel 保存 attached TUI 当前帧所需的全部状态。
// 调用层级：LaunchAttachedTUI -> NewAppModel -> Bubble Tea Update/View。
// 设计边界：UI 只持有 remote client 回调和渲染快照，不直接持有 Runtime 或 Agent。
type AppModel struct {
	program      *tea.Program
	modelName    string
	sessionID    string
	remoteSubmit func(string) error
	remoteStop   func() error
	ctx          context.Context

	width  int
	height int
	busy   bool

	viewport  viewport.Model
	input     textarea.Model
	entries   []conversationEntry
	viewText  string
	toolCalls int
	lastTool  string

	currentAssistant   int
	currentStatus      string
	remoteTurn         int
	spinnerFrame       int
	spinnerPending     bool
	renderPending      bool
	remoteDisconnected bool
	lastInput          string
	pendingInput       string
	escPending         bool
	lastEscAt          time.Time
	quitPending        bool
	lastQuitAt         time.Time
	autoScroll         bool
	metaCache          cachedMeta
	introInfo          introInfo
	picker             *pickerState
}

type pickerState struct {
	Kind     string
	Provider string
	Options  []string
	Cursor   int
}

type cachedMeta struct {
	Workdir  string
	Git      gitMeta
	LoadedAt time.Time
}

type assistantTokenMsg struct {
	token string
}

type assistantThinkingMsg struct {
	token string
}

type assistantDoneMsg struct{}

type assistantErrorMsg struct {
	err error
}

type toolEventMsg struct {
	event toolevent.ToolEvent
}

type remoteEventMsg struct{ event agentd.ProcessEvent }

type remoteDisconnectedMsg struct{}

type spinnerTickMsg struct{}

type renderTickMsg struct{}

type locationLoadedMsg struct {
	workdir string
	git     gitMeta
}

type remoteSubmitResultMsg struct {
	text string
	err  error
}

type remoteStopResultMsg struct {
	err error
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var (
	colorGreen   = lipgloss.Color("#9ece6a")
	colorBlue    = lipgloss.Color("#7aa2f7")
	colorPurple  = lipgloss.Color("#bb9af7")
	colorOrange  = lipgloss.Color("#DFA241")
	colorYellow  = lipgloss.Color("#F2C14E")
	colorGray    = lipgloss.Color("#565f89")
	colorWhite   = lipgloss.Color("#e2e1f1")
	colorCommand = colorOrange
	colorResult  = lipgloss.Color("#cdd6f4")
	colorError   = lipgloss.Color("#f38ba8")
	colorInputBg = lipgloss.Color("#404a4f")
	colorInputFg = lipgloss.Color("#dce4e3")
	colorMuted   = lipgloss.Color("#93a799")

	mainViewStyle = lipgloss.NewStyle().
			Padding(0, 0)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	inputShellStyle = lipgloss.NewStyle().
			Background(colorInputBg).
			Foreground(colorInputFg).
			Padding(0, 0)

	slashHintStyle = lipgloss.NewStyle().
			Foreground(colorGray)
)

type slashCommandHint struct {
	Name  string
	Usage string
	Desc  string
}

var slashCommandHints = []slashCommandHint{
	{Name: "/skill", Usage: "/skill <list|get|reload>", Desc: "skills"},
	{Name: "/compress", Usage: "/compress", Desc: "context"},
	{Name: "/mcp", Usage: "/mcp <list|add|remove|enable|disable>", Desc: "mcp servers"},
	{Name: "/session", Usage: "/session <new|list|id>", Desc: "sessions"},
	{Name: "/stop", Usage: "/stop", Desc: "stop current run"},
	{Name: "/model", Usage: "/model [name]", Desc: "models from daemon"},
	{Name: "/provider", Usage: "/provider [name]", Desc: "select and authenticate provider"},
}

// NewAppModel 创建 attached TUI 的初始模型。
func NewAppModel(ctx context.Context, modelName string, sessionID string) *AppModel {
	vp := viewport.New(0, 0)
	// viewport 自身支持滚轮，但还需要 LaunchAttachedTUI 开启 Bubble Tea mouse mode。
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = 2

	input := textarea.New()
	input.Placeholder = ""
	input.Focus()
	input.ShowLineNumbers = false
	input.SetHeight(1)
	input.Prompt = "❯ "
	// 输入框使用参考 tmux 对话窗口的低对比深灰条，避免大白块抢视觉焦点。
	input.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(colorYellow).Background(colorInputBg).Bold(true)
	input.FocusedStyle.Text = lipgloss.NewStyle().Foreground(colorInputFg).Background(colorInputBg)
	input.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(colorGray).Background(colorInputBg)
	input.FocusedStyle.CursorLine = lipgloss.NewStyle().Foreground(colorInputFg).Background(colorInputBg)
	input.FocusedStyle.CursorLineNumber = lipgloss.NewStyle().Foreground(colorGray).Background(colorInputBg)
	input.BlurredStyle = input.FocusedStyle

	return &AppModel{
		modelName:        modelName,
		sessionID:        sessionID,
		ctx:              ctx,
		viewport:         vp,
		input:            input,
		entries:          []conversationEntry{{Role: roleIntro}},
		currentAssistant: -1,
		currentStatus:    "idle",
		autoScroll:       true,
	}
}

// Init 返回 Bubble Tea 启动时需要执行的光标闪烁和异步元信息加载命令。
func (m *AppModel) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.loadRuntimeLocationCmd(), m.loadIntroCmd())
}

// Update 处理 Bubble Tea 消息并更新 TUI 状态机。
// 消息按布局、本地 Agent 输出、daemon attach 事件、工具事件、picker 模态和键鼠输入分层处理。
func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// 布局事件只更新尺寸和缓存视图，不触发 Agent 状态变化。
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		m.refreshView()
		return m, nil
	case assistantTokenMsg:
		// 高频 token 只改状态并排队渲染，避免每个 chunk 全量重绘历史。
		if !m.busy {
			return m, nil
		}
		if m.currentAssistant == -1 || m.currentAssistant >= len(m.entries) || m.entries[m.currentAssistant].Role != roleAssistant {
			m.entries = append(m.entries, conversationEntry{Role: roleAssistant, Content: msg.token})
			m.currentAssistant = len(m.entries) - 1
		} else if m.currentAssistant < len(m.entries) {
			m.entries[m.currentAssistant].Content += msg.token
		}
		m.currentStatus = "streaming"
		return m, tea.Batch(m.queueRender(), m.queueSpinner())
	case assistantThinkingMsg:
		if !m.busy {
			return m, nil
		}
		if len(m.entries) > 0 && m.entries[len(m.entries)-1].Role == roleThinking {
			m.entries[len(m.entries)-1].Content += msg.token
		} else {
			m.entries = append(m.entries, conversationEntry{Role: roleThinking, Content: msg.token})
		}
		m.currentAssistant = -1
		m.currentStatus = "thinking"
		return m, tea.Batch(m.queueRender(), m.queueSpinner())
	case assistantDoneMsg:
		m.busy = false
		m.currentAssistant = -1
		m.currentStatus = "idle"
		m.renderPending = false
		m.refreshView()
		return m, tea.Batch(m.loadRuntimeLocationCmd(), m.submitPendingInputCmd())
	case assistantErrorMsg:
		m.busy = false
		m.currentAssistant = -1
		m.currentStatus = "error"
		m.renderPending = false
		m.entries = append(m.entries, conversationEntry{Role: roleSystem, Content: "agent error: " + msg.err.Error()})
		m.refreshView()
		return m, tea.Batch(m.loadRuntimeLocationCmd(), m.submitPendingInputCmd())
	case remoteEventMsg:
		// attached 模式把 daemon 协议事件翻译成本地 TUI 状态，不直接访问 Runtime。
		event := msg.event
		if event.Turn > 0 {
			m.remoteTurn = event.Turn
		}
		switch event.Type {
		case agentd.ProcessEventUser:
			m.entries = append(m.entries, conversationEntry{Role: roleUser, Content: event.Text})
			m.refreshView()
			return m, nil
		case agentd.ProcessEventState:
			m.busy = event.Busy
			if event.Busy {
				m.currentStatus = "running"
				m.refreshView()
				return m, m.queueSpinner()
			}
			if m.currentStatus != "error" {
				m.currentStatus = "idle"
			}
			m.refreshView()
			return m, m.submitPendingInputCmd()
		case agentd.ProcessEventAssistant:
			return m.Update(assistantTokenMsg{token: event.Text})
		case agentd.ProcessEventThinking:
			return m.Update(assistantThinkingMsg{token: event.Text})
		case agentd.ProcessEventTool:
			return m.Update(toolEventMsg{event: toolevent.ToolEvent{Kind: event.Kind, Name: event.Name, Args: event.Args, Text: event.Text, Result: event.Result, Error: event.Error, Concurrent: event.Concurrent}})
		case agentd.ProcessEventSystem:
			m.busy = false
			m.currentStatus = "idle"
			m.entries = append(m.entries, conversationEntry{Role: roleSystem, Content: event.Text})
			m.refreshView()
			return m, nil
		case agentd.ProcessEventPicker:
			m.busy = false
			m.currentStatus = "select " + event.Kind
			m.picker = &pickerState{Kind: event.Kind, Provider: event.Name, Options: append([]string(nil), event.Options...)}
			m.refreshView()
			return m, nil
		case agentd.ProcessEventModel:
			m.busy = false
			m.modelName = event.Text
			m.currentStatus = "idle"
			m.refreshView()
			return m, nil
		case agentd.ProcessEventDone:
			return m.Update(assistantDoneMsg{})
		case agentd.ProcessEventError:
			message := fallback(event.Error, event.Text)
			return m.Update(assistantErrorMsg{err: fmt.Errorf("%s", message)})
		}
		return m, nil
	case remoteDisconnectedMsg:
		if m.remoteDisconnected {
			return m, nil
		}
		m.remoteDisconnected = true
		m.busy = false
		m.currentStatus = "disconnected"
		m.remoteSubmit = func(string) error { return fmt.Errorf("daemon disconnected") }
		m.remoteStop = func() error { return fmt.Errorf("daemon disconnected") }
		if len(m.entries) == 0 || m.entries[len(m.entries)-1].Content != "daemon disconnected" {
			m.entries = append(m.entries, conversationEntry{Role: roleSystem, Content: "daemon disconnected"})
		}
		m.renderPending = false
		m.refreshView()
		return m, nil
	case spinnerTickMsg:
		// spinner 只保留一个定时链，避免 token 密集时堆积大量 Tick。
		m.spinnerPending = false
		if m.busy {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			m.refreshView()
			return m, m.queueSpinner()
		}
		return m, nil
	case renderTickMsg:
		m.renderPending = false
		m.refreshView()
		return m, nil
	case locationLoadedMsg:
		m.metaCache = cachedMeta{Workdir: msg.workdir, Git: msg.git, LoadedAt: time.Now()}
		m.refreshView()
		return m, nil
	case introLoadedMsg:
		m.introInfo = msg.info
		m.refreshView()
		return m, nil
	case remoteSubmitResultMsg:
		if msg.err != nil {
			m.busy = false
			m.currentStatus = "error"
			m.entries = append(m.entries, conversationEntry{Role: roleSystem, Content: msg.err.Error()})
			m.refreshView()
			return m, nil
		}
		if strings.HasPrefix(strings.TrimSpace(msg.text), "/") {
			m.currentStatus = "idle"
			m.refreshView()
		}
		return m, nil
	case remoteStopResultMsg:
		if msg.err != nil {
			m.currentStatus = "error"
			m.entries = append(m.entries, conversationEntry{Role: roleSystem, Content: msg.err.Error()})
			m.refreshView()
		}
		return m, nil
	case toolEventMsg:
		if msg.event.Kind == "call" {
			m.toolCalls++
			m.lastTool = fallback(tools.DisplayName(msg.event.Name), msg.event.Name)
		}
		m.applyToolEvent(msg.event)
		m.currentAssistant = -1
		m.refreshView()
		if msg.event.Kind == "call" {
			return m, m.queueSpinner()
		}
		return m, nil
	case tea.MouseMsg:
		// 鼠标滚轮只驱动历史 viewport，不抢输入框焦点。
		// tea.WithMouseCellMotion 负责把终端滚轮事件送到这里。
		before := m.viewport.YOffset
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		if m.viewport.YOffset != before {
			m.autoScroll = m.viewport.AtBottom()
			return m, cmd
		}
		return m, cmd
	case tea.KeyMsg:
		// tmux mouse 转义序列偶尔会以普通按键漏进来；这里直接吞掉，
		// 避免 `[<65;...M` 之类的滚轮事件污染输入框。
		if isMouseEscapeKey(msg.String()) {
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c":
			if confirm(&m.quitPending, &m.lastQuitAt, quitConfirmDelay) {
				return m, tea.Quit
			}
			m.input.Reset()
			m.picker = nil
			m.currentStatus = "input cleared; ctrl+c again to quit"
			m.refreshView()
			return m, nil
		case "ctrl+d":
			return m, tea.Quit
		}
		if m.picker != nil {
			// picker 是模态输入；存在时不让普通快捷键和 textarea 继续消费按键。
			switch msg.String() {
			case "up", "ctrl+p":
				if m.picker.Cursor > 0 {
					m.picker.Cursor--
				}
				m.refreshView()
				return m, nil
			case "down", "ctrl+n":
				if m.picker.Cursor+1 < len(m.picker.Options) {
					m.picker.Cursor++
				}
				m.refreshView()
				return m, nil
			case "esc":
				m.picker = nil
				m.currentStatus = "idle"
				m.refreshView()
				return m, nil
			case "enter":
				if len(m.picker.Options) == 0 || m.remoteSubmit == nil {
					return m, nil
				}
				value := m.picker.Options[m.picker.Cursor]
				command := "/" + m.picker.Kind + " " + value
				if m.picker.Kind == "model" {
					command = "/model " + m.picker.Provider + "/" + value
				}
				m.picker = nil
				m.currentStatus = "command"
				m.refreshView()
				return m, remoteSubmitCmd(m.remoteSubmit, command)
			}
			return m, nil
		}
		switch msg.String() {
		case "ctrl+u":
			m.input.Reset()
			m.quitPending = false
			m.refreshView()
			return m, nil
		case "esc":
			if confirm(&m.escPending, &m.lastEscAt, quitConfirmDelay) {
				return m, tea.Quit
			}
			m.currentStatus = "esc again to quit"
			m.refreshView()
			return m, nil
		case "enter":
			if msg.Paste {
				break
			}
			return m, m.submit()
		case "tab":
			text := strings.TrimSpace(m.input.Value())
			if strings.HasPrefix(text, "/") && !strings.Contains(text, " ") {
				if matches := slashHintMatches(text); len(matches) == 1 {
					m.input.SetValue(matches[0].Name + " ")
					m.input.CursorEnd()
				}
				m.refreshView()
			}
			return m, nil
		case "pgdown", "ctrl+f":
			m.autoScroll = m.viewport.AtBottom()
			m.viewport.ViewDown()
			m.autoScroll = m.viewport.AtBottom()
			m.refreshView()
			return m, nil
		case "pgup", "ctrl+b":
			m.autoScroll = false
			m.viewport.ViewUp()
			m.refreshView()
			return m, nil
		case "down", "ctrl+n":
			m.viewport.LineDown(1)
			m.autoScroll = m.viewport.AtBottom()
			m.refreshView()
			return m, nil
		case "up":
			// 只保留最近一次提交，满足快速重复输入；多级 shell history 暂不引入。
			if m.lastInput != "" {
				m.input.SetValue(m.lastInput)
			}
			return m, nil
		case "ctrl+p":
			m.autoScroll = false
			m.viewport.LineUp(1)
			m.refreshView()
			return m, nil
		case "end":
			m.viewport.GotoBottom()
			m.autoScroll = true
			m.refreshView()
			return m, nil
		case "home":
			m.viewport.GotoTop()
			m.autoScroll = false
			m.refreshView()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if _, ok := msg.(tea.KeyMsg); ok {
		m.escPending = false
		m.quitPending = false
		if m.cleanInputValue() {
			m.refreshView()
		}
	}
	m.viewport, _ = m.viewport.Update(msg)
	m.autoScroll = m.viewport.AtBottom()
	return m, cmd
}

func (m *AppModel) submitPendingInputCmd() tea.Cmd {
	if m.busy || m.remoteSubmit == nil || strings.TrimSpace(m.pendingInput) == "" {
		return nil
	}
	text := m.pendingInput
	m.pendingInput = ""
	m.busy = true
	m.currentStatus = "submitting queued"
	m.refreshView()
	return tea.Batch(remoteSubmitCmd(m.remoteSubmit, text), m.queueSpinner())
}

func (m *AppModel) queueSpinner() tea.Cmd {
	if !m.busy || m.spinnerPending {
		return nil
	}
	m.spinnerPending = true
	return tickSpinner()
}

func (m *AppModel) queueRender() tea.Cmd {
	if m.renderPending {
		return nil
	}
	m.renderPending = true
	return tea.Tick(33*time.Millisecond, func(time.Time) tea.Msg { return renderTickMsg{} })
}

func (m *AppModel) loadRuntimeLocationCmd() tea.Cmd {
	workdir := m.metaCache.Workdir
	if workdir == "" || workdir == "-" {
		if cwd, err := os.Getwd(); err == nil {
			workdir = cwd
		}
	}
	if workdir == "" || workdir == "-" {
		return nil
	}
	return func() tea.Msg {
		return locationLoadedMsg{workdir: workdir, git: readGitMeta(workdir)}
	}
}
