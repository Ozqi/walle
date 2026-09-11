// 本文件承载 attached TUI 的视图渲染、宽度处理和输入辅助函数。
// 调用方：AppModel.Update/View、render.go 和 tool_render.go；不访问 Runtime 或 Agent。
// 全局状态：mouseEscapePattern 过滤 tmux mouse escape；其它函数只使用 AppModel 的本地渲染状态。
package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	ansi "github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

var mouseEscapePattern = regexp.MustCompile(`(?:\x1b)?\[<[0-9;]*[mM]?`)

func isMouseEscapeKey(text string) bool {
	return mouseEscapePattern.MatchString(text)
}

func stripMouseEscapeSequences(text string) string {
	return mouseEscapePattern.ReplaceAllString(text, "")
}

func (m *AppModel) cleanInputValue() bool {
	value := m.input.Value()
	cleaned := stripMouseEscapeSequences(value)
	if cleaned == value {
		return false
	}
	m.input.SetValue(cleaned)
	m.input.CursorEnd()
	return true
}

// confirm 处理“短时间内二次按键确认”的通用状态。
func confirm(pending *bool, last *time.Time, within time.Duration) bool {
	now := time.Now()
	if *pending && now.Sub(*last) <= within {
		*pending = false
		return true
	}
	*pending = true
	*last = now
	return false
}

// View 渲染当前 TUI 画面，并保留底部占位行避免输入框贴边。
func (m *AppModel) View() string {
	m.resize()
	// 当前布局保持单列：历史记录在上，输入框附近承载运行状态。
	mainView := renderMainPane(m)
	mainHeight := max(1, m.height-1)
	bottomPad := statusBarStyle.Width(max(1, m.width)).Render(strings.Repeat(" ", max(1, m.width)))
	return lipgloss.JoinVertical(lipgloss.Left,
		renderFixedLines(strings.Split(mainView, "\n"), max(1, m.width), mainHeight),
		bottomPad,
	)
}

func (m *AppModel) resize() {
	if m.width <= 0 {
		m.width = defaultTUIWidth
	}
	if m.height <= 0 {
		m.height = defaultTUIHeight
	}

	mainWidth := max(20, m.width)
	m.input.SetWidth(max(8, mainWidth-2))
	m.input.SetHeight(1)
	m.viewport.Width = max(8, mainWidth)
	m.viewport.Height = max(1, m.height-1-m.reservedMainHeight(mainWidth))
}

// reservedMainHeight 返回 viewport 之外的主界面行数。
// 这里按真实渲染文本计数，避免窄屏 slash hint 或长输入换行后挤到输入框下方。
func (m *AppModel) reservedMainHeight(width int) int {
	headerHeight := 1
	inputHeight := renderedLineCount(renderInputBar(m.input.View(), width))
	footerHeight := renderedLineCount(renderInputFooter(m.snapshot(), m.sessionID, width))
	slashHeight := renderedLineCount(m.renderSlashHint(max(12, width-4)))
	return headerHeight + slashHeight + inputHeight + 1 + footerHeight + 1
}

func renderedLineCount(text string) int {
	if strings.TrimSpace(stripANSI(text)) == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

// refreshView 重新渲染所有会话条目，并在 auto-scroll 开启时保持贴底。
func (m *AppModel) refreshView() {
	m.resize()
	contentWidth := max(8, m.viewport.Width)
	stickToBottom := m.autoScroll || m.viewport.AtBottom() || m.viewport.TotalLineCount() <= m.viewport.Height
	parts := make([]string, 0, len(m.entries))
	for i := range m.entries {
		if m.entries[i].CreatedAt == "" {
			m.entries[i] = entryNow(m.entries[i])
		}
		parts = append(parts, m.renderConversationEntryCached(i, contentWidth))
	}
	m.viewText = strings.Join(parts, "\n\n")
	m.viewport.SetContent(m.viewText)
	if stickToBottom {
		m.viewport.GotoBottom()
		m.autoScroll = true
	}
}

// renderIntroEntry 渲染 session 开头的固定说明；它作为历史条目参与滚动。
func (m *AppModel) renderIntroEntry(width int) string {
	width = max(24, width)
	title := lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render("walle 启动上下文")
	muted := lipgloss.NewStyle().Foreground(colorMuted)
	lines := []string{title, muted.Render("预载提示词")}
	for _, prompt := range fallbackList(m.introInfo.Prompts, []string{"main.md"}) {
		lines = append(lines, "  • "+lipgloss.NewStyle().Foreground(colorWhite).Render(prompt))
	}
	lines = append(lines, "", muted.Render("预载 Skills"))
	skills := fallbackList(m.introInfo.Skills, []string{"未发现已加载 Skill"})
	for i, skill := range skills {
		if i >= maxHintRows {
			lines = append(lines, muted.Render(fmt.Sprintf("  • ... 还有 %d 个", len(skills)-i)))
			break
		}
		lines = append(lines, "  • "+wrapVisibleText(skill, max(8, width-4)))
	}
	return strings.Join(lines, "\n")
}

func fallbackList(items []string, fallbackItems []string) []string {
	if len(items) == 0 {
		return fallbackItems
	}
	return items
}

// snapshot 汇总输入框附近状态区需要的数据。
// 调用层级：View -> renderMainPane -> snapshot。
func (m *AppModel) snapshot() statusSnapshot {
	snapshot := statusSnapshot{Runtime: runtimeMeta{
		Busy:           m.busy,
		State:          animatedStateLabel(m.busy, m.currentStatus, m.spinnerFrame),
		Turn:           m.remoteTurn,
		ScrollPercent:  int(m.viewport.ScrollPercent() * 100),
		ToolCallsTotal: m.toolCalls,
		LastToolName:   m.lastTool,
		PendingInput:   strings.TrimSpace(m.pendingInput) != "",
		SessionID:      m.sessionID,
	}}
	snapshot.Runtime.Workdir, snapshot.Runtime.Git = m.runtimeLocation()
	return snapshot
}

func (m *AppModel) runtimeLocation() (string, gitMeta) {
	if m.metaCache.Workdir != "" {
		return m.metaCache.Workdir, m.metaCache.Git
	}
	return "-", gitMeta{}
}

func renderState(state string, busy bool) string {
	color := colorGreen
	if busy {
		color = colorYellow
	}
	if strings.Contains(state, "error") {
		color = colorError
	}
	return lipgloss.NewStyle().Foreground(color).Render(state)
}

func (m *AppModel) renderSlashHint(width int) string {
	if m.picker != nil {
		if len(m.picker.Options) == 0 {
			return slashHintStyle.Width(width).Render("No options available")
		}
		lines := []string{"Select " + m.picker.Kind}
		start := max(0, m.picker.Cursor-maxHintRows/2)
		end := min(len(m.picker.Options), start+maxHintRows)
		start = max(0, end-maxHintRows)
		for index := start; index < end; index++ {
			option := m.picker.Options[index]
			prefix := "  "
			if index == m.picker.Cursor {
				prefix = "❯ "
			}
			lines = append(lines, prefix+option)
		}
		if len(m.picker.Options) > end {
			lines = append(lines, fmt.Sprintf("  ... %d more", len(m.picker.Options)-end))
		}
		return slashHintStyle.Width(width).Render(strings.Join(lines, "\n"))
	}
	raw := m.input.Value()
	text := strings.TrimSpace(raw)
	if !strings.HasPrefix(text, "/") {
		return ""
	}
	matches := slashHintMatches(text)
	if len(matches) == 0 {
		return slashHintStyle.Width(width).Render("unknown slash command")
	}
	parts := make([]string, 0, len(matches))
	for _, hint := range matches {
		text := hint.Usage + "  " + hint.Desc
		if width < 72 {
			text = hint.Name + "  " + hint.Desc
		}
		parts = append(parts, wrapVisibleText(text, max(8, width-2)))
	}
	return slashHintStyle.Width(width).Render(strings.Join(parts, "\n"))
}

func slashHintMatches(input string) []slashCommandHint {
	fields := strings.Fields(input)
	prefix := input
	if len(fields) > 0 {
		prefix = fields[0]
	}
	if prefix == "/" {
		return slashCommandHints
	}
	matches := make([]slashCommandHint, 0, len(slashCommandHints))
	for _, hint := range slashCommandHints {
		if strings.HasPrefix(hint.Name, prefix) {
			matches = append(matches, hint)
		}
	}
	return matches
}

func (m *AppModel) renderConversationEntryCached(index int, width int) string {
	entry := m.entries[index]
	if entry.Role == roleIntro {
		return m.renderIntroEntry(width)
	}
	frame := -1
	if entry.Role == roleHint && entry.ToolState == "running" {
		frame = m.spinnerFrame
	}
	key := entry.renderKey()
	if entry.renderCacheText != "" && entry.renderCacheKey == key && entry.renderCacheWidth == width && entry.renderCacheFrame == frame {
		return entry.renderCacheText
	}
	rendered := m.renderConversationEntry(entry, width)
	m.entries[index].renderCacheKey = key
	m.entries[index].renderCacheWidth = width
	m.entries[index].renderCacheFrame = frame
	m.entries[index].renderCacheText = rendered
	return rendered
}

func (entry conversationEntry) renderKey() string {
	return strings.Join([]string{entry.Role, entry.Content, entry.CreatedAt, entry.ToolName, entry.ToolIntent, entry.ToolArgs, entry.ToolKey, entry.ToolState, entry.ToolOutput, entry.SystemTitle}, "\x00")
}

func (m *AppModel) renderConversationEntry(entry conversationEntry, width int) string {
	innerWidth := max(8, width-2)
	switch entry.Role {
	case roleUser:
		body := compactParagraph(strings.TrimSpace(entry.Content))
		return renderUserEntry(body, width)
	case roleAssistant:
		content := strings.TrimRight(renderMarkdownForTerminal(normalizeAssistantContent(entry.Content), true), "\n")
		return renderAssistantEntry(content, width)
	case roleIntro:
		return m.renderIntroEntry(width)
	case roleHint:
		return m.renderToolHintEntry(entry, width)
	case roleThinking:
		return renderThinkingEntry(entry.Content, width)
	case roleSystem:
		return renderSystemEntry(entry.SystemTitle, entry.Content, width)
	default:
		return renderIndentedEntry(wrapVisibleText(strings.TrimSpace(entry.Content), innerWidth))
	}
}

func renderIndentedEntry(rendered string) string {
	return indentLines(rendered, "  ", "  ")
}

func renderAssistantEntry(content string, width int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	body := wrapVisibleText(content, max(8, width-4))
	return lipgloss.NewStyle().Foreground(colorBlue).Bold(true).Render("⏺ ") + indentLines(body, "", "  ")
}

func renderSystemEntry(title string, content string, width int) string {
	content = strings.TrimSpace(content)
	label := "recap"
	if strings.TrimSpace(title) != "" {
		label = truncateMiddle(strings.TrimSpace(title), 40)
		content = compactCommandOutput(content, 12)
	}
	prefix := lipgloss.NewStyle().Foreground(colorMuted).Render("※ " + label)
	if content == "" {
		return prefix
	}
	body := loggerColorLines(wrapVisibleText(content, max(8, width-4)), colorMuted)
	return prefix + ": " + indentLines(body, "", "  ")
}

func compactCommandOutput(content string, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) <= maxLines || maxLines < 4 {
		return content
	}
	tail := strings.TrimSpace(lines[len(lines)-1])
	headCount := maxLines - 2
	result := append([]string{}, lines[:headCount]...)
	result = append(result, fmt.Sprintf("... %d lines omitted ...", len(lines)-maxLines+1))
	if tail != "" {
		result = append(result, tail)
	}
	return strings.Join(result, "\n")
}

func renderUserEntry(content string, width int) string {
	content = strings.TrimSpace(content)
	prefix := lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render("❯ ")
	if content == "" {
		return prefix
	}
	lineWidth := max(8, width-lipgloss.Width(prefix))
	lines := wrapVisibleLines(content, lineWidth)
	for i, line := range lines {
		if i == 0 {
			lines[i] = prefix + lipgloss.NewStyle().Foreground(colorWhite).Render(line)
			continue
		}
		lines[i] = strings.Repeat(" ", lipgloss.Width(prefix)) + line
	}
	return strings.Join(lines, "\n")
}

func renderThinkingEntry(content string, width int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	body := loggerColorLines(wrapVisibleText(content, max(8, width-2)), colorGray)
	return lipgloss.NewStyle().Foreground(colorMuted).Render("✶ ") + body
}

func indentLines(text string, firstPrefix string, nextPrefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == 0 {
			lines[i] = firstPrefix + line
		} else {
			lines[i] = nextPrefix + line
		}
	}
	return strings.Join(lines, "\n")
}

func wrapVisibleLines(line string, width int) []string {
	if width <= 0 || lipgloss.Width(line) <= width || strings.TrimSpace(stripANSI(line)) == "" {
		return []string{line}
	}
	if line != stripANSI(line) {
		line = stripANSI(line)
	}
	lines := strings.Split(ansi.Wrap(line, width, " \t"), "\n")
	if len(lines) == 0 {
		return []string{line}
	}
	return lines
}

func wrapVisibleText(text string, width int) string {
	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines)*2)
	for _, line := range lines {
		if line == "" {
			result = append(result, "")
			continue
		}
		wrapped := wrapVisibleLines(line, width)
		result = append(result, wrapped...)
	}
	return strings.Join(result, "\n")
}

func stripANSI(text string) string {
	return ansiPattern.ReplaceAllString(text, "")
}

func renderViewportPane(vp viewport.Model, content string) string {
	width := max(1, vp.Width)
	height := max(1, vp.Height)
	if vp.TotalLineCount() <= vp.Height {
		return renderFixedLines(strings.Split(content, "\n"), width, height)
	}
	return renderFixedLines(strings.Split(vp.View(), "\n"), width, height)
}

func renderFixedLines(lines []string, width int, height int) string {
	fixed := make([]string, 0, height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		line = clipVisibleLine(line, width)
		padding := width - lipgloss.Width(line)
		if padding > 0 {
			line += strings.Repeat(" ", padding)
		}
		fixed = append(fixed, line)
	}
	return strings.Join(fixed, "\n")
}

func clipVisibleLine(line string, width int) string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	var b strings.Builder
	visible := 0
	for i := 0; i < len(line); {
		if line[i] == '\x1b' {
			if end := strings.IndexByte(line[i:], 'm'); end >= 0 {
				b.WriteString(line[i : i+end+1])
				i += end + 1
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		rw := runewidth.RuneWidth(r)
		if visible+rw > width {
			break
		}
		b.WriteRune(r)
		visible += rw
		i += size
	}
	return b.String()
}

func fallback(value string, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

// truncateMiddle 截断中间部分
func truncateMiddle(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	half := (maxLen - 3) / 2
	return string(runes[:half]) + "..." + string(runes[len(runes)-half:])
}

func normalizeAssistantContent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	for strings.Contains(content, "\n\n") {
		content = strings.ReplaceAll(content, "\n\n", "\n")
	}
	return content
}

func animatedStateLabel(busy bool, state string, frame int) string {
	state = fallback(state, "idle")
	if !busy || state == "idle" || len(spinnerFrames) == 0 {
		return state
	}
	return spinnerFrames[frame%len(spinnerFrames)] + " " + state
}

func tickSpinner() tea.Cmd {
	return tea.Tick(180*time.Millisecond, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}
