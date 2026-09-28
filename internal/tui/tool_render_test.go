package tui

import (
	"strings"
	"testing"

	"github.com/Ozqi/walle/internal/toolevent"
	"github.com/charmbracelet/lipgloss"
)

func TestFormatToolArgsSummarySkipsEmptyArgs(t *testing.T) {
	for _, args := range []string{"", "{}", "null", `"null"`} {
		if got := formatToolArgsSummary(args); got != "" {
			t.Fatalf("formatToolArgsSummary(%q) = %q, want empty", args, got)
		}
	}
}

func TestCleanToolTitleRemovesNullSuffix(t *testing.T) {
	for _, title := range []string{"read_file(null)", `exec_shell("null")`} {
		if got := cleanToolTitle(title); got != title[:strings.Index(title, "(")] {
			t.Fatalf("cleanToolTitle(%q) = %q", title, got)
		}
	}
}

func TestFormatToolArgsSummaryCoversBaseToolArgs(t *testing.T) {
	cases := map[string]string{
		`{"path":"internal/tui/app.go","offset":1,"limit":3}`:               "internal/tui/app.go",
		`{"file_path":"/tmp/walle-ui-test.txt"}`:                            "/tmp/walle-ui-test.txt",
		`{"command":"pwd"}`:                                                 "pwd",
		`{"pattern":"cleanToolTitle","path":"internal/tui/tool_render.go"}`: "internal/tui/tool_render.go",
	}
	for args, want := range cases {
		if got := formatToolArgsSummary(args); !strings.Contains(got, want) {
			t.Fatalf("formatToolArgsSummary(%q) = %q, want it to contain %q", args, got, want)
		}
	}
}

func TestApplyToolEventCoversBaseToolSummaries(t *testing.T) {
	cases := []struct {
		name   string
		args   string
		result string
		want   []string
	}{
		{name: "base.read_file", args: `{"path":"internal/tui/app.go","offset":1,"limit":3}`, result: `{"content":"package tui\n","total_lines":10}`, want: []string{"read_file", "total lines: 10"}},
		{name: "base.read_md", args: `{"path":"README.md","action":"list_headings"}`, result: `{"path":"README.md","action":"list_headings","headings":[{"level":1,"title":"walle","line":1},{"level":2,"title":"Usage","line":8}],"total_lines":42}`, want: []string{"read_md", "total lines: 42", "headings: 2", "L1 H1 walle"}},
		{name: "base.exec_shell", args: `{"command":"pwd"}`, result: `{"stdout":"/tmp\n","stderr":"","returncode":0}`, want: []string{"exec_shell", "exit code: 0", "/tmp"}},
		{name: "base.glob", args: `{"pattern":"internal/tui/*.go"}`, result: `{"files":["internal/tui/app.go","internal/tui/render.go"]}`, want: []string{"glob", "matches: 2", "internal/tui/app.go"}},
		{name: "base.grep", args: `{"pattern":"cleanToolTitle","path":"internal/tui/tool_render.go"}`, result: `{"matches":[{"file":"internal/tui/tool_render.go","line":284,"text":"func cleanToolTitle(title string) string {"}]}`, want: []string{"grep", "matches: 1", "tool_render.go:284"}},
		{name: "base.list_dir", args: `{"path":"internal/tui"}`, result: `{"files":[{"path":"internal/tui/app.go","is_dir":false},{"path":"internal/tui","is_dir":true}]}`, want: []string{"list_dir", "entries: 2", "internal/tui/app.go"}},
		{name: "base.write_file", args: `{"path":"/tmp/walle-ui-test.txt"}`, result: `{"success":true,"message":"Written to /tmp/walle-ui-test.txt","bytes":2}`, want: []string{"write_file", "bytes: 2", "Written to /tmp/walle-ui-test.txt"}},
		{name: "base.edit", args: `{"file_path":"/tmp/walle-ui-test.txt","old_string":"old","new_string":"new"}`, result: `{"path":"/tmp/walle-ui-test.txt","replacements":1}`, want: []string{"edit", "- old", "+ new"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := NewAppModel(t.Context(), "test-model", "test-session")
			model.applyToolEvent(toolevent.ToolEvent{Kind: "call", Name: tc.name, Args: tc.args})
			model.applyToolEvent(toolevent.ToolEvent{Kind: "result", Name: tc.name, Args: tc.args, Text: toolevent.FormatToolResult(tc.name, tc.args, tc.result), Result: tc.result})
			if len(model.entries) != 2 {
				t.Fatalf("entries = %d, want 2", len(model.entries))
			}
			rendered := model.renderToolHintEntry(model.entries[1], 100)
			for _, want := range tc.want {
				if !strings.Contains(rendered, want) {
					t.Fatalf("rendered %s missing %q:\n%s", tc.name, want, rendered)
				}
			}
		})
	}
}

func TestToolRenderingKeepsExtremeFailuresReadable(t *testing.T) {
	cases := []struct {
		name  string
		width int
		args  string
		err   string
	}{
		{name: "long path", width: 80, args: `{"path":"/tmp/` + strings.Repeat("very-long-path-segment/", 8) + `missing-file.txt"}`, err: strings.Repeat("extra diagnostic detail ", 10)},
		{name: "narrow command", width: 40, args: `{"command":"` + strings.Repeat("printf extremely-long-token ", 6) + `"}`, err: strings.Repeat("stderr chunk ", 12)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := NewAppModel(t.Context(), "test-model", "test-session")
			longErr := "tool failed: " + tc.err
			model.applyToolEvent(toolevent.ToolEvent{Kind: "call", Name: "base.read_file", Args: tc.args})
			model.applyToolEvent(toolevent.ToolEvent{Kind: "error", Name: "base.read_file", Args: tc.args, Error: longErr, Text: "✗ read_file failed: " + longErr})

			rendered := stripANSI(model.renderToolHintEntry(model.entries[1], tc.width))
			for _, line := range strings.Split(rendered, "\n") {
				if lipgloss.Width(line) > tc.width {
					t.Fatalf("tool error line width = %d, want <= %d:\n%s", lipgloss.Width(line), tc.width, rendered)
				}
			}
			if !strings.Contains(rendered, "✘ read_file") || !strings.Contains(rendered, "...") {
				t.Fatalf("long error render should keep tool and truncation marker:\n%s", rendered)
			}
		})
	}
}

func TestApplyToolEventRendersError(t *testing.T) {
	model := NewAppModel(t.Context(), "test-model", "test-session")
	args := `{"path":"missing.txt"}`
	model.applyToolEvent(toolevent.ToolEvent{Kind: "call", Name: "base.read_file", Args: args})
	model.applyToolEvent(toolevent.ToolEvent{Kind: "error", Name: "base.read_file", Args: args, Error: "file not found", Text: "✗ read_file failed: file not found"})

	if len(model.entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(model.entries))
	}
	rendered := model.renderToolHintEntry(model.entries[1], 100)
	for _, want := range []string{"read_file", "file not found"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("error render missing %q:\n%s", want, rendered)
		}
	}
}
