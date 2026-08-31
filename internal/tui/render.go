package tui

import (
	"fmt"
	"strings"

	"github.com/Ozqi/walle/internal/tools"
	"github.com/charmbracelet/lipgloss"
)

func renderMainPane(m *AppModel) string {
	width := max(20, m.width)
	snapshot := m.snapshot()
	header := renderTopStatus(snapshot, m.modelName, width)
	conversationHeight := max(1, m.viewport.Height)
	conversation := lipgloss.NewStyle().Height(conversationHeight).Render(renderViewportPane(m.viewport, m.viewText))
	inputBlock := renderInputBar(m.input.View(), width)
	slashHint := m.renderSlashHint(max(12, width-4))
	footer := renderInputFooter(snapshot, m.sessionID, width)
	blocks := []string{
		conversation,
		header,
	}
	if slashHint != "" {
		blocks = append(blocks, slashHint)
	}
	blocks = append(blocks, inputBlock)
	if footer != "" {
		blocks = append(blocks, footer)
	}
	content := lipgloss.JoinVertical(lipgloss.Left, blocks...)
	return mainViewStyle.Width(width).Render(content)
}

// renderInputBar 渲染单行输入条，避免宽屏下整行边框造成闪烁和视觉压迫。
// 参数：inputView 是 textarea 当前输出；width 是终端主列宽度。
func renderInputBar(inputView string, width int) string {
	width = max(12, width)
	return inputShellStyle.Width(width).Render(compactInputView(inputView))
}

func compactInputView(inputView string) string {
	lines := strings.Split(inputView, "\n")
	for len(lines) > 1 && isEmptyInputPromptLine(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func isEmptyInputPromptLine(line string) bool {
	plain := strings.TrimSpace(stripANSI(line))
	return plain == "" || plain == ">"
}

// renderTopStatus 渲染输入框上方的高频运行状态。
// 参数：snapshot 为远端运行快照；modelName 为 attach 时或 model 事件更新的模型名。
func renderTopStatus(snapshot statusSnapshot, modelName string, width int) string {
	meta := snapshot.Runtime
	labelWidth := max(16, width/3)
	if width < 72 {
		labelWidth = 22
	}
	parts := []string{
		lipgloss.NewStyle().Foreground(colorCommand).Render(truncateMiddle(fallback(modelName, "-"), labelWidth)),
		renderState(meta.State, meta.Busy),
	}
	if meta.Turn > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorPurple).Render(fmt.Sprintf("turn %d", meta.Turn)))
	}
	if meta.ToolCallsTotal > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorYellow).Render(fmt.Sprintf("tools %d", meta.ToolCallsTotal)))
	}
	if width >= 72 && meta.LastToolName != "" {
		last := truncateMiddle(fallback(tools.DisplayName(meta.LastToolName), meta.LastToolName), 24)
		parts = append(parts, lipgloss.NewStyle().Foreground(colorYellow).Render("last "+last))
	}
	label := strings.Join(parts, lipgloss.NewStyle().Faint(true).Render(" · "))
	return renderStatusRule(label, width)
}

func renderStatusRule(label string, width int) string {
	width = max(12, width)
	labelWidth := lipgloss.Width(label)
	if labelWidth+3 >= width {
		return label
	}
	line := strings.Repeat("─", max(1, width-labelWidth-2)) + " " + label
	return lipgloss.NewStyle().Foreground(colorGray).Render(line)
}

// renderInputFooter 渲染输入框下方的低频上下文状态。
// 参数：snapshot 为运行快照；width 为当前主列宽度。
func renderInputFooter(snapshot statusSnapshot, _ string, width int) string {
	meta := snapshot.Runtime
	if width < 72 {
		parts := make([]string, 0, 4)
		if meta.Workdir != "" && meta.Workdir != "-" {
			parts = append(parts, lipgloss.NewStyle().Foreground(colorWhite).Render(truncateMiddle(meta.Workdir, 24)))
		}
		if meta.Git.Repo {
			branch := truncateMiddle(fallback(meta.Git.Branch, "detached"), 12)
			if meta.Git.Dirty {
				branch += "*"
			}
			if meta.Git.Worktree {
				parts = append(parts, lipgloss.NewStyle().Foreground(colorGreen).Render("worktree "+branch))
			} else {
				parts = append(parts, lipgloss.NewStyle().Foreground(colorBlue).Render("git "+branch))
			}
		}
		if meta.PendingInput {
			parts = append(parts, lipgloss.NewStyle().Foreground(colorYellow).Render("queued"))
		}
		if meta.ScrollPercent < 100 {
			parts = append(parts, lipgloss.NewStyle().Foreground(colorPurple).Render(fmt.Sprintf("scroll %d%%", meta.ScrollPercent)))
		}
		return strings.Join(parts, lipgloss.NewStyle().Faint(true).Render(" · "))
	}
	parts := make([]string, 0, 8)
	if meta.Workdir != "" && meta.Workdir != "-" {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorWhite).Render(truncateMiddle(meta.Workdir, 36)))
	}
	if meta.Git.Repo {
		branch := truncateMiddle(fallback(meta.Git.Branch, "detached"), 18)
		prefix := "git "
		color := colorBlue
		if meta.Git.Worktree {
			prefix = "worktree "
			color = colorGreen
		}
		if meta.Git.Dirty {
			branch += "*"
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(color).Render(prefix+branch))
		if meta.Git.Shortstat != "" {
			parts = append(parts, lipgloss.NewStyle().Foreground(colorError).Render("diff "+truncateMiddle(meta.Git.Shortstat, 20)))
		}
	}
	if meta.PendingInput {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorYellow).Render("queued"))
	}
	if meta.ScrollPercent < 100 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorPurple).Render(fmt.Sprintf("scroll %d%%", meta.ScrollPercent)))
	}
	return strings.Join(parts, lipgloss.NewStyle().Faint(true).Render(" · "))
}
