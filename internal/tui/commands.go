package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// submit 将输入提交给 attached daemon。
// 本地只截获 /detach 和 /stop，其它 slash command 也交给 daemon session。
func (m *AppModel) submit() tea.Cmd {
	text := strings.TrimSpace(stripMouseEscapeSequences(m.input.Value()))
	if text == "" {
		return nil
	}
	if text == "/detach" {
		m.lastInput = text
		m.rememberInput(text)
		m.input.Reset()
		return tea.Quit
	}
	if m.remoteDisconnected {
		m.currentStatus = "disconnected"
		m.refreshView()
		return nil
	}
	m.lastInput = text
	m.rememberInput(text)
	m.input.Reset()
	if text == "/stop" {
		if m.remoteStop == nil {
			m.entries = append(m.entries, conversationEntry{Role: roleSystem, Content: "/stop is not available"})
			m.currentStatus = "error"
			m.refreshView()
			return nil
		}
		m.currentStatus = "stopping"
		m.refreshView()
		return remoteStopCmd(m.remoteStop)
	}
	isSlash := strings.HasPrefix(text, "/")
	if m.busy && !isSlash {
		m.pendingInput = text
		m.echoUserInput(text)
		m.currentStatus = "queued"
		m.refreshView()
		if m.remoteSubmit != nil {
			return remoteSubmitCmd(m.remoteSubmit, text)
		}
		return nil
	}
	if m.remoteSubmit == nil {
		m.entries = append(m.entries, conversationEntry{Role: roleSystem, Content: "daemon is not connected"})
		m.currentStatus = "error"
		m.refreshView()
		return nil
	}
	if !isSlash {
		m.echoUserInput(text)
		m.startRun("submitting")
		m.showAssistantWaiting()
	} else {
		m.currentStatus = "command"
	}
	m.refreshView()
	return tea.Batch(remoteSubmitCmd(m.remoteSubmit, text), m.queueSpinner())
}

func remoteSubmitCmd(submit func(string) error, text string) tea.Cmd {
	return func() tea.Msg {
		return remoteSubmitResultMsg{text: text, err: submit(text)}
	}
}

func remoteStopCmd(stop func() error) tea.Cmd {
	return func() tea.Msg {
		return remoteStopResultMsg{err: stop()}
	}
}
