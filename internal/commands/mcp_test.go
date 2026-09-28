// 功能：验证 /mcp 命令对用户级 MCP 配置文件的增删改查逻辑。
// 调用方：go test ./internal/commands；通过临时 HOME 隔离配置写入。
// 全局状态：测试会临时覆盖 HOME，只写入 t.TempDir() 下的 .walle/mcp.json。
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleMCPMutationsUseIsolatedHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".walle")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp.json"), []byte(`{"servers":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := HandleMCP("/mcp add bad-name echo"); err == nil {
		t.Fatal("HandleMCP accepted an invalid MCP server name")
	}

	steps := []struct {
		cmd  string
		want string
	}{
		{cmd: "/mcp add demo echo hello", want: "MCP server 'demo' added and enabled"},
		{cmd: "/mcp list", want: "mcp.demo.* [enabled]: echo [hello]"},
		{cmd: "/mcp disable demo", want: "MCP server 'demo' disabled"},
		{cmd: "/mcp enable demo", want: "MCP server 'demo' enabled"},
		{cmd: "/mcp remove demo", want: "MCP server 'demo' removed"},
		{cmd: "/mcp list", want: "No MCP servers configured"},
	}
	for _, step := range steps {
		t.Run(step.cmd, func(t *testing.T) {
			got, err := HandleMCP(step.cmd)
			if err != nil {
				t.Fatalf("HandleMCP(%q) error: %v", step.cmd, err)
			}
			if !strings.Contains(got, step.want) {
				t.Fatalf("HandleMCP(%q) = %q, want it to contain %q", step.cmd, got, step.want)
			}
		})
	}
}
