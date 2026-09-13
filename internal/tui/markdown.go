package tui

import (
	"regexp"
	"strings"

	"github.com/Ozqi/walle/internal/logger"
	"github.com/charmbracelet/lipgloss"
)

func renderMarkdownForTerminal(input string, color bool) string {
	blocks := splitMarkdownBlocks(input)
	if len(blocks) == 0 {
		return ""
	}

	rendered := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if strings.TrimSpace(block) == "" {
			continue
		}
		rendered = append(rendered, renderMarkdownBlock(block, color))
	}
	return strings.Join(rendered, "\n")
}

func splitMarkdownBlocks(input string) []string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil
	}

	lines := strings.Split(trimmed, "\n")
	blocks := make([]string, 0)
	var current []string
	inCodeBlock := false

	flush := func() {
		if len(current) == 0 {
			return
		}
		blocks = append(blocks, strings.Join(current, "\n"))
		current = nil
	}

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "```") {
			if inCodeBlock {
				current = append(current, line)
				inCodeBlock = false
				flush()
			} else {
				flush()
				inCodeBlock = true
				current = append(current, line)
			}
			continue
		}

		if inCodeBlock {
			current = append(current, line)
			continue
		}

		if trimmedLine == "" {
			flush()
			continue
		}

		// 表格行连续收集
		if isTableLine(trimmedLine) {
			if len(current) > 0 && !isTableLine(strings.TrimSpace(current[len(current)-1])) {
				flush()
			}
			current = append(current, line)
			continue
		}
		// 非表格行，如果当前累积的是表格行，先 flush
		if len(current) > 0 && isTableLine(strings.TrimSpace(current[len(current)-1])) {
			flush()
		}

		if isStandaloneMarkdownLine(trimmedLine) {
			flush()
			blocks = append(blocks, line)
			continue
		}

		current = append(current, line)
	}

	flush()
	return blocks
}

func isStandaloneMarkdownLine(line string) bool {
	return strings.HasPrefix(line, "#") || strings.HasPrefix(line, ">") || isListLine(line)
}

func isTableLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")
}

func isTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !isTableLine(trimmed) {
		return false
	}
	// 表格分隔行: |---|---| 或 |:---:|---:| 等
	cells := strings.Split(trimmed, "|")
	for _, cell := range cells {
		trimmed := strings.TrimSpace(cell)
		if trimmed == "" {
			continue
		}
		for _, ch := range trimmed {
			if ch != '-' && ch != ':' && ch != ' ' {
				return false
			}
		}
		if len(trimmed) < 1 {
			return false
		}
	}
	return true
}

func isListLine(line string) bool {
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		return true
	}
	for i := 0; i < len(line); i++ {
		if line[i] < '0' || line[i] > '9' {
			return i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' '
		}
	}
	return false
}

func renderMarkdownBlock(block string, color bool) string {
	trimmed := strings.TrimSpace(block)
	switch {
	case strings.HasPrefix(trimmed, "```"):
		return renderCodeBlock(trimmed, color)
	case strings.HasPrefix(trimmed, "#"):
		return applyInlineMarkdown(colorHeading(trimmed), color)
	case strings.HasPrefix(trimmed, ">"):
		return renderQuoteBlock(block, color)
	case isListLine(trimmed):
		return colorListLine(block, color)
	case isTableLine(trimmed):
		return renderTable(block, color)
	default:
		return applyInlineMarkdown(compactParagraph(trimmed), color)
	}
}

func compactParagraph(text string) string {
	parts := strings.Fields(strings.ReplaceAll(text, "\n", " "))
	return strings.Join(parts, " ")
}

func renderCodeBlock(block string, color bool) string {
	if !color {
		return block
	}
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			lines[i] = logger.Gray(line)
		} else if strings.HasPrefix(line, "+") {
			lines[i] = lipgloss.NewStyle().Foreground(colorGreen).Background(lipgloss.Color("#2F3A30")).Render(line)
		} else if strings.HasPrefix(line, "-") {
			lines[i] = lipgloss.NewStyle().Foreground(colorError).Background(lipgloss.Color("#3B2D26")).Render(line)
		} else {
			lines[i] = lipgloss.NewStyle().Foreground(colorResult).Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

// renderTable 渲染 markdown 表格，保留 Markdown 的管线形态并对齐列宽。
func renderTable(block string, color bool) string {
	lines := strings.Split(strings.TrimSpace(block), "\n")
	if len(lines) < 1 {
		return block
	}

	var rawRows [][]string
	sepIdx := -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !isTableLine(trimmed) {
			continue
		}
		cells := parseTableRow(trimmed)
		if isTableSeparator(trimmed) {
			sepIdx = len(rawRows)
			continue
		}
		rawRows = append(rawRows, cells)
	}

	if len(rawRows) == 0 {
		return block
	}

	// 计算列数
	numCols := 0
	for _, row := range rawRows {
		if len(row) > numCols {
			numCols = len(row)
		}
	}

	// 计算每列最大显示宽度。不能用 len()，中文和宽字符会导致表格错位。
	colWidths := make([]int, numCols)
	for _, row := range rawRows {
		for c, cell := range row {
			cellWidth := lipgloss.Width(cell)
			if c < numCols && cellWidth > colWidths[c] {
				colWidths[c] = cellWidth
			}
		}
	}

	var result []string
	for r, row := range rawRows {
		parts := make([]string, numCols)
		for c := 0; c < numCols; c++ {
			cell := ""
			if c < len(row) {
				cell = row[c]
			}
			padding := colWidths[c] - lipgloss.Width(cell)
			if padding < 0 {
				padding = 0
			}
			parts[c] = " " + cell + strings.Repeat(" ", padding) + " "
		}
		line := "|" + strings.Join(parts, "|") + "|"
		if color {
			if r == 0 {
				line = logger.Bold(line)
			} else {
				line = logger.Cyan(line)
			}
		}
		result = append(result, line)
		if r == 0 && sepIdx >= 0 {
			sepParts := make([]string, numCols)
			for c := 0; c < numCols; c++ {
				sepParts[c] = " " + strings.Repeat("-", max(3, colWidths[c])) + " "
			}
			sep := "|" + strings.Join(sepParts, "|") + "|"
			if color {
				sep = logger.Gray(sep)
			}
			result = append(result, sep)
		}
	}

	return strings.Join(result, "\n")
}

// parseTableRow 解析表格行的单元格
func parseTableRow(line string) []string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "|") {
		line = line[1:]
	}
	if strings.HasSuffix(line, "|") {
		line = line[:len(line)-1]
	}
	cells := strings.Split(line, "|")
	for i, cell := range cells {
		cells[i] = strings.TrimSpace(cell)
	}
	return cells
}

func colorHeading(line string) string {
	trimmed := strings.TrimSpace(line)
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	text := strings.TrimSpace(trimmed[level:])
	return logger.Bold(logger.Cyan(text))
}

func renderQuoteBlock(block string, color bool) string {
	lines := strings.Split(strings.TrimSpace(block), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, colorQuoteLine(line, color))
	}
	return strings.Join(out, "\n")
}

func colorQuoteLine(line string, color bool) string {
	leading, rest := splitLeadingSpace(line)
	rest = strings.TrimSpace(rest)
	level := 0
	for strings.HasPrefix(rest, ">") {
		level++
		rest = strings.TrimSpace(strings.TrimPrefix(rest, ">"))
	}
	if level == 0 {
		level = 1
	}
	marker := strings.Repeat("> ", level)
	text := applyInlineMarkdown(rest, color)
	if !color {
		return leading + marker + text
	}
	return leading + logger.Gray(marker) + text
}

func colorListLine(line string, color bool) string {
	leading, rest := splitLeadingSpace(line)
	marker, content := splitListMarker(strings.TrimSpace(rest))
	content = applyInlineMarkdown(content, color)
	if !color {
		return leading + marker + content
	}
	return leading + logger.Yellow(marker) + content
}

func splitLeadingSpace(line string) (string, string) {
	idx := 0
	for idx < len(line) && (line[idx] == ' ' || line[idx] == '\t') {
		idx++
	}
	return line[:idx], line[idx:]
}

func splitListMarker(line string) (string, string) {
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		return line[:2], line[2:]
	}
	for i := 0; i < len(line); i++ {
		if line[i] == '.' && i+1 < len(line) && line[i+1] == ' ' {
			return line[:i+2], line[i+2:]
		}
	}
	return "", line
}

var (
	inlineCodePattern = regexp.MustCompile("`([^`]+)`")
	boldPattern       = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	linkPattern       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

func applyInlineMarkdown(text string, color bool) string {
	if !color || text == "" {
		return text
	}
	text = linkPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := linkPattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		return logger.Cyan(parts[1]) + logger.Gray(" ("+parts[2]+")")
	})
	text = inlineCodePattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := inlineCodePattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		return logger.Yellow("`" + parts[1] + "`")
	})
	text = boldPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := boldPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		return logger.Bold(parts[1])
	})
	return text
}
