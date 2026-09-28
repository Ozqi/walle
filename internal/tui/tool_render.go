package tui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Ozqi/walle/internal/toolevent"
	"github.com/Ozqi/walle/internal/tools"
	"github.com/charmbracelet/lipgloss"
)

func (m *AppModel) applyToolEvent(event toolevent.ToolEvent) {
	displayName := fallback(tools.DisplayName(event.Name), event.Name)
	key := toolEventKey(event.Name, event.Args)
	summary := formatToolArgsSummary(event.Args)
	intent := toolIntent(event.Name, event.Args)

	// result/error 依赖 tool name + args 回填最近的 running 行；找不到时补一条完成记录，避免丢事件。
	switch event.Kind {
	case "call":
		m.entries = append(m.entries, conversationEntry{Role: roleHint, ToolName: displayName, ToolIntent: intent, ToolArgs: summary, ToolKey: key, ToolState: "running", ToolOutput: "running..."})
	case "result":
		idx := m.findRunningToolEntry(key, event.Name)
		output := summarizeToolEventOutput(event)
		if idx < 0 {
			m.entries = append(m.entries, conversationEntry{Role: roleHint, ToolName: displayName, ToolIntent: intent, ToolArgs: summary, ToolKey: key, ToolState: "done", ToolOutput: output})
			return
		}
		m.entries[idx].ToolState = "done"
		m.entries[idx].ToolOutput = output
	case "error":
		idx := m.findRunningToolEntry(key, event.Name)
		output := summarizeToolEventOutput(event)
		if idx < 0 {
			m.entries = append(m.entries, conversationEntry{Role: roleHint, ToolName: displayName, ToolIntent: intent, ToolArgs: summary, ToolKey: key, ToolState: "error", ToolOutput: output})
			return
		}
		m.entries[idx].ToolState = "error"
		m.entries[idx].ToolOutput = output
	default:
		m.entries = append(m.entries, conversationEntry{Role: roleHint, ToolName: displayName, ToolIntent: intent, ToolArgs: summary, ToolKey: key, ToolState: "done", ToolOutput: strings.TrimSpace(stripANSI(event.Text))})
	}
}

func toolIntent(name string, args string) string {
	var raw map[string]interface{}
	if json.Unmarshal([]byte(strings.TrimSpace(args)), &raw) == nil {
		if action, ok := raw["action"]; ok {
			return strings.ReplaceAll(toolArgValue(action), "_", " ")
		}
	}
	switch tools.DisplayName(name) {
	case "exec_shell":
		return "run shell command"
	case "read_file":
		return "read file"
	case "read_md":
		return "read Markdown"
	case "write_file":
		return "write file"
	case "edit":
		return "edit file"
	case "grep":
		return "search text"
	case "glob":
		return "match paths"
	case "list_dir":
		return "list directory"
	default:
		return ""
	}
}

func (m *AppModel) findRunningToolEntry(key string, name string) int {
	displayName := tools.DisplayName(name)
	for i := len(m.entries) - 1; i >= 0; i-- {
		entry := m.entries[i]
		if entry.Role != roleHint || entry.ToolState != "running" {
			continue
		}
		if key != "" && entry.ToolKey == key {
			return i
		}
		if entry.ToolName == name || entry.ToolName == displayName {
			return i
		}
	}
	return -1
}

func toolEventKey(name string, args string) string {
	return name + "\x00" + strings.TrimSpace(args)
}

func formatToolArgsSummary(args string) string {
	args = strings.TrimSpace(args)
	if args == "" || args == "{}" || args == "null" || args == `"null"` {
		return ""
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return "(" + truncateMiddle(args, 180) + ")"
	}
	if raw == nil {
		return ""
	}
	if value := firstToolArg(raw, "file_path", "path", "command", "pattern"); value != "" {
		return "(" + truncateMiddle(value, 120) + ")"
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+truncateMiddle(toolArgValue(raw[key]), 120))
	}
	return "(" + strings.Join(parts, ",") + ")"
}

func firstToolArg(raw map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		rawValue, ok := raw[key]
		if !ok || rawValue == nil {
			continue
		}
		value := strings.TrimSpace(toolArgValue(rawValue))
		if value != "" && value != "null" {
			return value
		}
	}
	return ""
}

func toolArgValue(v interface{}) string {
	switch vv := v.(type) {
	case string:
		return vv
	case float64:
		return fmt.Sprintf("%g", vv)
	case bool:
		if vv {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	}
}

func summarizeToolEventOutput(event toolevent.ToolEvent) string {
	clean := strings.TrimSpace(stripANSI(event.Text))
	// 兼容 logger 旧文本块格式（●/⎿/✗），也兼容当前 ToolEvent.Text 的短摘要。
	entry := parseLegacyToolBlock(clean)
	switch event.Kind {
	case "result":
		displayName := tools.DisplayName(event.Name)
		if displayName == "edit" {
			if diff, ok := summarizeEditDiff(event.Args); ok {
				return diff
			}
		}
		if displayName == "read_md" {
			if summary, ok := summarizeReadMDResult(event.Result); ok {
				return summary
			}
		}
		if displayName == "write_file" {
			if summary, ok := summarizeWriteFileResult(event.Result); ok {
				return summary
			}
		}
		fields := append([]string{}, entry.Result...)
		if len(fields) == 0 {
			fields = strings.Split(strings.TrimSpace(strings.TrimPrefix(clean, "⎿ ")), "\n")
		}
		return compactOutputLines(fields, 4)
	case "error":
		if entry.Error != "" {
			return compactOutputLines([]string{entry.Error}, 2)
		}
		if event.Error != "" {
			return "error: " + truncateMiddle(event.Error, 180)
		}
		return compactOutputLines(strings.Split(clean, "\n"), 2)
	default:
		return compactOutputLines(strings.Split(clean, "\n"), 3)
	}
}

const (
	editDiffMaxLines = 3
	editDiffLineLen  = 120
)

func summarizeReadMDResult(result string) (string, bool) {
	var output struct {
		Action     string `json:"action"`
		Heading    string `json:"heading"`
		StartLine  int    `json:"start_line"`
		EndLine    int    `json:"end_line"`
		Content    string `json:"content"`
		TotalLines int    `json:"total_lines"`
		Headings   []struct {
			Level int    `json:"level"`
			Title string `json:"title"`
			Line  int    `json:"line"`
		} `json:"headings"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(result)), &output) != nil || output.Action == "" {
		return "", false
	}

	lines := []string{fmt.Sprintf("total lines: %d", output.TotalLines)}
	switch output.Action {
	case "list_headings":
		lines = append(lines, fmt.Sprintf("headings: %d", len(output.Headings)))
		for _, heading := range output.Headings[:min(len(output.Headings), 2)] {
			lines = append(lines, fmt.Sprintf("L%d H%d %s", heading.Line, heading.Level, heading.Title))
		}
	case "read_section":
		if output.Heading != "" {
			lines = append(lines, fmt.Sprintf("section: %s (%d-%d)", output.Heading, output.StartLine, output.EndLine))
		}
		if content := strings.TrimSpace(output.Content); content != "" {
			lines = append(lines, compactOutputLines(strings.Split(content, "\n"), 2))
		}
	case "replace_section", "delete_section":
		lines = append(lines, fmt.Sprintf("%s: %s (%d-%d)", output.Action, output.Heading, output.StartLine, output.EndLine))
	}
	return compactOutputLines(lines, 4), true
}

func summarizeWriteFileResult(result string) (string, bool) {
	var output struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Bytes   int    `json:"bytes"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(result)), &output) != nil || output.Message == "" {
		return "", false
	}
	return compactOutputLines([]string{fmt.Sprintf("bytes: %d", output.Bytes), output.Message}, 3), true
}

func summarizeEditDiff(args string) (string, bool) {
	var input struct {
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if json.Unmarshal([]byte(args), &input) != nil || input.OldString == "" || input.NewString == "" {
		return "", false
	}

	oldLines := strings.Split(input.OldString, "\n")
	newLines := strings.Split(input.NewString, "\n")
	start := 0
	for start < len(oldLines) && start < len(newLines) && oldLines[start] == newLines[start] {
		start++
	}
	oldEnd, newEnd := len(oldLines), len(newLines)
	for oldEnd > start && newEnd > start && oldLines[oldEnd-1] == newLines[newEnd-1] {
		oldEnd--
		newEnd--
	}

	lines := appendEditDiffLines(nil, "- ", oldLines[start:oldEnd])
	lines = appendEditDiffLines(lines, "+ ", newLines[start:newEnd])
	if len(lines) == 0 {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

func appendEditDiffLines(dst []string, prefix string, lines []string) []string {
	limit := min(len(lines), editDiffMaxLines)
	for _, line := range lines[:limit] {
		dst = append(dst, prefix+truncateMiddle(strings.TrimSuffix(line, "\r"), editDiffLineLen))
	}
	if len(lines) > limit {
		dst = append(dst, prefix+"...")
	}
	return dst
}

func compactOutputLines(lines []string, limit int) string {
	result := make([]string, 0, limit)
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(line, "⎿ "))
		if line == "" {
			continue
		}
		result = append(result, truncateMiddle(line, 160))
		if len(result) >= limit {
			break
		}
	}
	if len(result) == 0 {
		return "(no output)"
	}
	if len(lines) > len(result) {
		result = append(result, "...")
	}
	return strings.Join(result, "\n")
}

func (m *AppModel) renderToolHintEntry(entry conversationEntry, width int) string {
	stateIcon := "⏺"
	stateColor := colorBlue
	if entry.ToolState == "error" {
		stateIcon = "✘"
		stateColor = colorError
	}

	name := cleanToolTitle(fallback(entry.ToolName, "tool"))
	args := strings.TrimSpace(entry.ToolArgs)
	title := name
	if args != "" {
		title += args
	}
	title = clipVisibleLine(cleanToolTitle(title), max(8, width-2))
	header := lipgloss.NewStyle().Foreground(stateColor).Bold(true).Render(stateIcon) + " " + lipgloss.NewStyle().Foreground(colorWhite).Render(title)

	output := strings.TrimSpace(entry.ToolOutput)
	if output == "" {
		return header
	}
	lineWidth := max(8, width-6)
	lines := wrapVisibleText(compactToolOutputForWidth(output, lineWidth), lineWidth)
	if entry.ToolState == "error" {
		lines = loggerColorLines(lines, colorError)
	} else if entry.ToolName == "edit" {
		lines = colorEditDiffLines(lines)
	} else {
		lines = loggerColorLines(lines, colorResult)
	}
	return header + "\n" + indentLines(lines, "  ⎿  ", "     ")
}

func cleanToolTitle(title string) string {
	title = strings.TrimSpace(title)
	for _, suffix := range []string{"(null)", `("null")`} {
		title = strings.TrimSuffix(title, suffix)
	}
	return strings.TrimSpace(title)
}

func colorEditDiffLines(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		color := colorResult
		if strings.HasPrefix(line, "- ") {
			color = colorError
		}
		lines[i] = lipgloss.NewStyle().Foreground(color).Render(line)
	}
	return strings.Join(lines, "\n")
}

func loggerColorLines(text string, color lipgloss.Color) string {
	style := lipgloss.NewStyle().Foreground(color)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

func compactToolOutputForWidth(output string, width int) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		result = append(result, truncateMiddle(line, max(24, width)))
	}
	return strings.Join(result, "\n")
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// toolEntry 表示解析后的工具条目
type toolEntry struct {
	Name   string
	Args   []string
	Result []string
	Error  string
}

func parseLegacyToolBlock(text string) toolEntry {
	clean := strings.TrimSpace(ansiPattern.ReplaceAllString(text, ""))
	if clean == "" {
		return toolEntry{}
	}

	lines := strings.Split(clean, "\n")
	entry := toolEntry{}
	seenResult := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "● ") {
			rest := strings.TrimPrefix(line, "● ")
			rest = strings.TrimSuffix(rest, " [并发]")
			entry.Name = extractToolNameFromCall(rest)
			continue
		}
		if strings.HasPrefix(line, "⎿ ") {
			seenResult = true
			line = strings.TrimSpace(strings.TrimPrefix(line, "⎿ "))
		}
		if strings.HasPrefix(line, "✗") {
			entry.Error = strings.TrimSpace(line)
			seenResult = true
			continue
		}
		if !seenResult {
			entry.Args = append(entry.Args, line)
			continue
		}
		entry.Result = append(entry.Result, line)
	}
	return entry
}

// extractToolName 从工具调用行提取工具名称
func extractToolNameFromCall(text string) string {
	// 格式: ToolName(args...) 或 ToolName
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, "("); idx > 0 {
		text = text[:idx]
	}
	return strings.TrimSpace(text)
}
