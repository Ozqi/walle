// 功能：验证 base 工具通过 Eino InvokableRun 真实执行文件、检索、Markdown 和 shell 操作。
// 调用方：go test ./internal/tools；每个测试使用 t.TempDir 隔离文件系统副作用。
// 全局状态：无；exec_shell 只在临时 workspace 内执行无破坏命令。
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentctx "github.com/Ozqi/walle/internal/context"
	"github.com/Ozqi/walle/internal/mcp"
	"github.com/Ozqi/walle/internal/skill"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestBaseToolsInvokeRealOperations(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	write, err := NewWriteFileTool(root)
	write = mustTool(t, write, err)
	read, err := NewReadFileTool(root)
	read = mustTool(t, read, err)
	glob, err := NewGlobTool(root)
	glob = mustTool(t, glob, err)
	grep, err := NewGrepTool(root)
	grep = mustTool(t, grep, err)
	listDir, err := NewListDirTool(root)
	listDir = mustTool(t, listDir, err)
	edit, err := NewEditTool(root)
	edit = mustTool(t, edit, err)
	execShell, err := NewExecShellTool(root)
	execShell = mustTool(t, execShell, err)
	readMD, err := NewReadMDTool(root)
	readMD = mustTool(t, readMD, err)

	var writeOut WriteFileOutput
	invokeJSON(t, ctx, write, `{"path":"notes/a.txt","content":"alpha\nbeta\nalpha\n"}`, &writeOut)
	if !writeOut.Success || writeOut.Bytes == 0 {
		t.Fatalf("write_file output = %#v, want success", writeOut)
	}

	var readOut ReadFileOutput
	invokeJSON(t, ctx, read, `{"path":"notes/a.txt","offset":2,"limit":1}`, &readOut)
	if readOut.TotalLines != 3 || !strings.Contains(readOut.Content, "2\tbeta") {
		t.Fatalf("read_file output = %#v, want second line", readOut)
	}

	var globOut GlobOutput
	invokeJSON(t, ctx, glob, `{"path":"notes","pattern":"*.txt"}`, &globOut)
	if globOut.Count != 1 || filepath.Base(globOut.Files[0]) != "a.txt" {
		t.Fatalf("glob output = %#v, want notes/a.txt", globOut)
	}

	var grepOut GrepOutput
	invokeJSON(t, ctx, grep, `{"path":"notes","pattern":"beta"}`, &grepOut)
	if grepOut.Count != 1 || grepOut.Matches[0].Line != 2 || grepOut.Matches[0].Text == "" {
		t.Fatalf("grep output = %#v, want one beta match", grepOut)
	}

	var listOut ListDirOutput
	invokeJSON(t, ctx, listDir, `{"path":"notes","recursive":false}`, &listOut)
	if listOut.Count != 1 || listOut.Files[0].Name != "a.txt" || listOut.Files[0].IsDir {
		t.Fatalf("list_dir output = %#v, want a.txt file", listOut)
	}

	var editOut EditOutput
	invokeJSON(t, ctx, edit, `{"path":"notes/a.txt","old_string":"beta","new_string":"BETA"}`, &editOut)
	if !editOut.Success || editOut.Replacements != 1 {
		t.Fatalf("edit output = %#v, want one replacement", editOut)
	}

	var shellOut ExecShellOutput
	invokeJSON(t, ctx, execShell, `{"command":"printf shell-ok"}`, &shellOut)
	if shellOut.ReturnCode != 0 || shellOut.Stdout != "shell-ok" {
		t.Fatalf("exec_shell output = %#v, want shell-ok", shellOut)
	}

	mdPath := filepath.Join(root, "doc.md")
	if err := os.WriteFile(mdPath, []byte("# Title\n\nintro\n\n## Detail\nbody\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var mdOut ReadMDOutput
	invokeJSON(t, ctx, readMD, `{"path":"doc.md","action":"list_headings"}`, &mdOut)
	if len(mdOut.Headings) != 2 || mdOut.Headings[1].Title != "Detail" {
		t.Fatalf("read_md headings = %#v, want Title and Detail", mdOut.Headings)
	}
}

func TestRegistryRegistersDesignedToolSet(t *testing.T) {
	ctx := context.Background()
	registry := NewRegistry()
	registry.SetWorkspaceRoot(t.TempDir())
	skillMgr := skill.NewManagerFromDirs()
	if err := registry.Init(skillMgr); err != nil {
		t.Fatal(err)
	}
	registry.RegisterContextTool(nil, t.TempDir())
	if err := registry.RegisterMCPTools("demo", &fakeMCPClient{}, []mcp.ToolSpec{{Name: "lookup"}}); err != nil {
		t.Fatal(err)
	}

	infos, err := registry.ToolInfos(ctx)
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
		"skill.skill", "context.context", "mcp.demo.lookup",
	} {
		if !got[name] {
			t.Fatalf("registered tools = %#v, missing %s", got, name)
		}
	}
}

func TestRegistryAllDesignedToolsInvoke(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "a.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte("# Title\n\nintro\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo skill\n---\n\nUse demo skill."), 0o644); err != nil {
		t.Fatal(err)
	}

	skillMgr := skill.NewManagerFromDirs(skill.Source{Scope: "project", Dir: filepath.Join(root, "skills")})
	if err := skillMgr.LoadSkills(); err != nil {
		t.Fatal(err)
	}
	mgr := agentctx.NewManager(t.TempDir())
	msgCtx, err := mgr.CreateContext("registry-tools")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.AddMessage(msgCtx, &schema.Message{Role: schema.User, Content: "hello registry"}); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	registry.SetWorkspaceRoot(root)
	if err := registry.Init(skillMgr); err != nil {
		t.Fatal(err)
	}
	registry.RegisterContextTool(nil, filepath.Join(root, "prompt"))
	client := &fakeMCPClient{}
	if err := registry.RegisterMCPTools("demo", client, []mcp.ToolSpec{{Name: "lookup", Description: "Lookup demo"}}); err != nil {
		t.Fatal(err)
	}

	runtimeCtx := agentctx.WithToolRuntime(ctx, mgr, msgCtx)
	calls := map[string]string{
		"base.read_file":  `{"path":"notes/a.txt","offset":1,"limit":1}`,
		"base.read_md":    `{"path":"doc.md","action":"list_headings"}`,
		"base.exec_shell": `{"command":"printf registry-shell"}`,
		"base.glob":       `{"path":"notes","pattern":"*.txt"}`,
		"base.edit":       `{"path":"notes/a.txt","old_string":"beta","new_string":"BETA"}`,
		"base.write_file": `{"path":"notes/generated.txt","content":"generated\n"}`,
		"base.grep":       `{"path":"notes","pattern":"alpha"}`,
		"base.list_dir":   `{"path":"notes","recursive":false}`,
		"skill.skill":     `{"action":"get","skill":"demo"}`,
		"context.context": `{"action":"inspect"}`,
		"mcp.demo.lookup": `{"query":"wall-e"}`,
	}
	seen := map[string]bool{}
	for _, item := range registry.All() {
		info, err := item.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		args, ok := calls[info.Name]
		if !ok {
			t.Fatalf("unexpected registry tool %q", info.Name)
		}
		callCtx := ctx
		if info.Name == "context.context" {
			callCtx = runtimeCtx
		}
		result, err := invokeRegistryTool(callCtx, item, args)
		if err != nil {
			t.Fatalf("%s InvokableRun error: %v", info.Name, err)
		}
		if strings.TrimSpace(result) == "" {
			t.Fatalf("%s returned empty result", info.Name)
		}
		seen[info.Name] = true
	}
	for name := range calls {
		if !seen[name] {
			t.Fatalf("registry tool %s was not invoked; seen=%#v", name, seen)
		}
	}
	if client.toolName != "lookup" || client.args != `{"query":"wall-e"}` {
		t.Fatalf("MCP client = %#v, want passthrough lookup", client)
	}
}

func TestNonBaseToolsInvokeRealOperations(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	skillDir := filepath.Join(root, "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo skill\n---\n\nUse demo skill."), 0o644); err != nil {
		t.Fatal(err)
	}
	skillMgr := skill.NewManagerFromDirs(skill.Source{Scope: "project", Dir: filepath.Join(root, "skills")})
	if err := skillMgr.LoadSkills(); err != nil {
		t.Fatal(err)
	}
	skillTool := &SkillTool{mgr: skillMgr}
	list, err := skillTool.InvokableRun(ctx, `{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "demo [project]: Demo skill") {
		t.Fatalf("skill list = %q, want demo skill", list)
	}
	gotSkill, err := skillTool.InvokableRun(ctx, `{"action":"get","skill":"demo"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotSkill, "# Skill: demo") || !strings.Contains(gotSkill, "Use demo skill.") {
		t.Fatalf("skill get = %q, want skill content", gotSkill)
	}

	mgr := agentctx.NewManager(t.TempDir())
	msgCtx, err := mgr.CreateContext("ctx-tool")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.AddMessage(msgCtx, &schema.Message{Role: schema.User, Content: "hello context"}); err != nil {
		t.Fatal(err)
	}
	contextTool := NewContextTool(nil, root)
	runtimeCtx := agentctx.WithToolRuntime(ctx, mgr, msgCtx)
	inspect, err := contextTool.InvokableRun(runtimeCtx, `{"action":"inspect"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inspect, "hello context") {
		t.Fatalf("context inspect = %q, want message preview", inspect)
	}
	pinned, err := contextTool.InvokableRun(runtimeCtx, `{"action":"pin","start":0,"end":0,"reason":"keep"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pinned, `"ok":true`) {
		t.Fatalf("context pin = %q, want ok", pinned)
	}
	if err := mgr.AddMessage(msgCtx, &schema.Message{Role: schema.Assistant, Content: "draft answer"}); err != nil {
		t.Fatal(err)
	}
	edited, err := contextTool.InvokableRun(runtimeCtx, `{"action":"edit","index":1,"content":"edited answer","reason":"fix"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(edited, `"ok":true`) {
		t.Fatalf("context edit = %q, want ok", edited)
	}
	audit, err := contextTool.InvokableRun(runtimeCtx, `{"action":"audit"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(audit, `"Op":"pin"`) || !strings.Contains(audit, `"Op":"edit"`) {
		t.Fatalf("context audit = %q, want pin and edit", audit)
	}
	compressed, err := contextTool.InvokableRun(runtimeCtx, `{"action":"compress","mode":"truncate"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compressed, `"action":"compress"`) || !strings.Contains(compressed, `"ok":true`) {
		t.Fatalf("context compress = %q, want ok", compressed)
	}

	client := &fakeMCPClient{}
	mcpTool := NewMCPTool("demo", client, mcp.ToolSpec{Name: "lookup", Description: "Lookup demo"})
	info, err := mcpTool.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "mcp.demo.lookup" {
		t.Fatalf("MCP tool name = %q, want mcp.demo.lookup", info.Name)
	}
	mcpResult, err := mcpTool.InvokableRun(ctx, `{"query":"wall-e"}`)
	if err != nil {
		t.Fatal(err)
	}
	if mcpResult != "mcp ok" || client.toolName != "lookup" || client.args != `{"query":"wall-e"}` {
		t.Fatalf("MCP result/client = %q/%#v, want passthrough", mcpResult, client)
	}
}

func invokeRegistryTool(ctx context.Context, item tool.BaseTool, args string) (string, error) {
	if enhanced, ok := item.(tool.EnhancedInvokableTool); ok {
		result, err := enhanced.InvokableRun(ctx, &schema.ToolArgument{Text: args})
		if err != nil {
			return "", err
		}
		if len(result.Parts) == 0 {
			return "", nil
		}
		return result.Parts[0].Text, nil
	}
	invokable, ok := item.(tool.InvokableTool)
	if !ok {
		return "", nil
	}
	return invokable.InvokableRun(ctx, args)
}

func mustTool(t *testing.T, tool tool.EnhancedInvokableTool, err error) tool.EnhancedInvokableTool {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

type fakeMCPClient struct {
	toolName string
	args     string
}

func (c *fakeMCPClient) CallTool(ctx context.Context, toolName string, arguments string) (string, error) {
	c.toolName = toolName
	c.args = arguments
	return "mcp ok", nil
}

func invokeJSON(t *testing.T, ctx context.Context, tool tool.EnhancedInvokableTool, args string, out any) {
	t.Helper()
	result, err := tool.InvokableRun(ctx, &schema.ToolArgument{Text: args})
	if err != nil {
		t.Fatalf("InvokableRun(%s): %v", args, err)
	}
	if len(result.Parts) != 1 || result.Parts[0].Text == "" {
		t.Fatalf("InvokableRun(%s) returned empty result: %#v", args, result)
	}
	if err := json.Unmarshal([]byte(result.Parts[0].Text), out); err != nil {
		t.Fatalf("decode result %q: %v", result.Parts[0].Text, err)
	}
}
