// 功能：验证 Runtime 内部 session 打开策略，不创建 LLM、不启动 daemon 或 MCP。
// 调用方：go test ./internal/runtime；使用临时 session store 覆盖默认、continue 和显式 session 路径。
// 全局状态：无；所有 session 文件写入 t.TempDir。
package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentctx "github.com/Ozqi/walle/internal/context"
	"github.com/cloudwego/eino/schema"
)

func TestRuntimeNewIgnoresUserMCPConfigByDefault(t *testing.T) {
	isolateRuntimeEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".walle")
	if err := os.MkdirAll(filepath.Join(configDir, "prompt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "prompt", "main.md"), []byte("system prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	mcpConfig := `{"servers":[{"name":"demo","command":"definitely-not-started","enabled":true}]}`
	if err := os.WriteFile(filepath.Join(configDir, "mcp.json"), []byte(mcpConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	rt, err := New(context.Background(), Options{ProjectDir: t.TempDir(), ModelRef: "deepseek/deepseek-flash"})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	infos, err := rt.ToolRegistry.ToolInfos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, info := range infos {
		got[info.Name] = true
	}
	for _, name := range []string{
		"base.read_file", "base.read_md", "base.exec_shell", "base.glob",
		"base.edit", "base.write_file", "base.grep", "base.list_dir",
		"skill.skill", "context.context",
	} {
		if !got[name] {
			t.Fatalf("runtime tools = %#v, missing %s", got, name)
		}
	}
	for name := range got {
		if strings.HasPrefix(name, "mcp.") {
			t.Fatalf("runtime unexpectedly registered MCP tool %s in default startup", name)
		}
	}
}

func isolateRuntimeEnv(t *testing.T) {
	t.Helper()
	old := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(key, "LLM_") || key == "DEEPSEEK_API_KEY" {
			old[key] = value
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		for key := range old {
			_ = os.Unsetenv(key)
		}
		for key, value := range old {
			_ = os.Setenv(key, value)
		}
	})
}

func TestOpenMessageCtxSessionSelection(t *testing.T) {
	mgr := agentctx.NewManager(t.TempDir())

	fresh, freshID, err := openMessageCtx(mgr, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == nil || freshID == "" {
		t.Fatalf("default open = (%#v, %q), want new bound session", fresh, freshID)
	}
	if err := mgr.AddMessage(fresh, &schema.Message{Role: schema.User, Content: "old"}); err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Millisecond)
	latest, latestID, err := openMessageCtx(mgr, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if latestID == "" || latestID == freshID {
		t.Fatalf("second default session id = %q, want new id distinct from %q", latestID, freshID)
	}
	if err := mgr.AddMessage(latest, &schema.Message{Role: schema.User, Content: "new"}); err != nil {
		t.Fatal(err)
	}

	continued, continuedID, err := openMessageCtx(mgr, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if continuedID != latestID {
		t.Fatalf("continue session id = %q, want latest %q", continuedID, latestID)
	}
	messages, err := mgr.GetMessages(continued)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "new" {
		t.Fatalf("continued messages = %#v, want latest session history", messages)
	}

	explicit, explicitID, err := openMessageCtx(mgr, freshID, false)
	if err != nil {
		t.Fatal(err)
	}
	if explicitID != freshID {
		t.Fatalf("explicit session id = %q, want %q", explicitID, freshID)
	}
	messages, err = mgr.GetMessages(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "old" {
		t.Fatalf("explicit messages = %#v, want selected session history", messages)
	}
}
