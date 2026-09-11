package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/Ozqi/walle/internal/tools"
	"github.com/charmbracelet/lipgloss"
)

func renderMainPane(m *AppModel) string {
	width := max(20, m.width)
	snapshot := m.snapshot()
	header := renderTopStatus(snapshot, width)
	conversationHeight := max(1, m.viewport.Height)
	conversation := lipgloss.NewStyle().Height(conversationHeight).Render(renderViewportPane(m.viewport, m.viewText))
	inputBlock := renderInputBar(m.input.View(), width)
	slashHint := m.renderSlashHint(max(12, width-4))
	footer := renderInputFooter(snapshot, m.modelName, width)
	blocks := []string{
		conversation,
		header,
	}
	if slashHint != "" {
		blocks = append(blocks, slashHint)
	}
	blocks = append(blocks, inputBlock, renderPlainRule(width))
	if footer != "" {
		blocks = append(blocks, footer)
	}
	blocks = append(blocks, renderModeHint(width))
	content := lipgloss.JoinVertical(lipgloss.Left, blocks...)
	return mainViewStyle.Width(width).Render(content)
}

// renderInputBar 渲染单行输入条；不铺满背景，保持接近 Claude Code 的轻量命令行形态。
// 参数：inputView 是 textarea 当前输出；width 是终端主列宽度。
func renderInputBar(inputView string, width int) string {
	width = max(12, width)
	return clipVisibleLine(compactInputView(inputView), width)
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
	return plain == "" || plain == ">" || plain == "❯"
}

// renderTopStatus 渲染输入框上方的状态分隔线；详细上下文放在 footer，贴近 Claude Code 布局。
func renderTopStatus(snapshot statusSnapshot, width int) string {
	meta := snapshot.Runtime
	parts := []string{renderState(meta.State, meta.Busy)}
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
	label = label + " ─"
	labelWidth := lipgloss.Width(label)
	if labelWidth+3 >= width {
		return label
	}
	line := strings.Repeat("─", max(1, width-labelWidth-2)) + " " + label
	return lipgloss.NewStyle().Foreground(colorGray).Render(line)
}

func renderPlainRule(width int) string {
	return lipgloss.NewStyle().Foreground(colorGray).Render(strings.Repeat("─", max(12, width)))
}

// renderInputFooter 渲染输入框下方的低频上下文状态。
// 参数：snapshot 为运行快照；modelName 为 attach 时或 model 事件更新的模型名。
func renderInputFooter(snapshot statusSnapshot, modelName string, width int) string {
	meta := snapshot.Runtime
	parts := make([]string, 0, 6)
	pathWidth := 24
	branchWidth := 12
	modelWidth := 22
	if width >= 72 {
		pathWidth = 36
		branchWidth = 18
		modelWidth = 30
	}
	if meta.Workdir != "" && meta.Workdir != "-" {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorWhite).Render(truncateMiddle(homePath(meta.Workdir), pathWidth)))
	}
	if meta.Git.Repo {
		branch := truncateMiddle(fallback(meta.Git.Branch, "detached"), branchWidth)
		if meta.Git.Dirty {
			branch += " *"
		}
		if meta.Git.Untracked {
			branch += "?"
		}
		if meta.Git.Worktree {
			branch = "worktree " + branch
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(colorBlue).Render(branch))
	}
	parts = append(parts, lipgloss.NewStyle().Foreground(colorCommand).Render(truncateMiddle(fallback(modelName, "-"), modelWidth)))
	if meta.ContextWindow > 0 || meta.TotalTokens > 0 || meta.PromptTokens > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorMuted).Render(formatTokenUsage(meta.PromptTokens, meta.TotalTokens)))
	}
	if meta.ContextWindow > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorMuted).Render(formatContextUsage(meta.TotalTokens, meta.ContextWindow)))
	}
	if meta.PendingInput {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorYellow).Render("queued"))
	}
	if meta.ScrollPercent < 100 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorPurple).Render(fmt.Sprintf("scroll %d%%", meta.ScrollPercent)))
	}
	return renderFooterParts(parts)
}

func formatTokenUsage(promptTokens int, totalTokens int) string {
	if promptTokens <= 0 && totalTokens <= 0 {
		return "tok 0i/0o"
	}
	if promptTokens > 0 && totalTokens > 0 {
		return fmt.Sprintf("tok %di/%do", promptTokens, totalTokens-promptTokens)
	}
	return fmt.Sprintf("tok %d", max(promptTokens, totalTokens))
}

func formatContextUsage(totalTokens int, contextWindow int) string {
	percent := totalTokens * 100 / contextWindow
	if percent == 0 && totalTokens > 0 {
		percent = 1
	}
	return fmt.Sprintf("ctx %d%% used", percent)
}

func renderFooterParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	separator := lipgloss.NewStyle().Faint(true).Render(" │ ")
	return "  " + strings.Join(parts, separator)
}

func renderModeHint(width int) string {
	hint := "  ⏵⏵ auto mode on (shift+tab to cycle)"
	if width >= 72 {
		hint += " · ← for agents"
	}
	return lipgloss.NewStyle().Foreground(colorMuted).Render(clipVisibleLine(hint, width))
}

func homePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(path, home) {
		return path
	}
	if path == home {
		return "~"
	}
	return "~" + strings.TrimPrefix(path, home)
}
