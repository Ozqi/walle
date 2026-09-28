// 功能：验证 OpenAI-compatible/Claude 工具名别名在请求和响应两侧保持可逆。
// 调用方：go test ./internal/llm；使用内存 fake model，不访问真实 provider。
// 全局状态：无。
package llm

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type fakeToolModel struct {
	tools []*schema.ToolInfo
	seen  []*schema.Message
}

func (m *fakeToolModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	clone := *m
	clone.tools = tools
	return &clone, nil
}

func (m *fakeToolModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.seen = input
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call_1", Type: "function", Function: schema.FunctionCall{Name: "base_read_file", Arguments: `{}`}}}}, nil
}

func (m *fakeToolModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.seen = input
	return schema.StreamReaderFromArray([]*schema.Message{{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call_1", Type: "function", Function: schema.FunctionCall{Name: "base_read_file", Arguments: `{}`}}}}}), nil
}

func TestSafeToolNameModelAliasesAndRestoresToolCalls(t *testing.T) {
	base := &fakeToolModel{}
	bound, err := withSafeToolNames(base).WithTools([]*schema.ToolInfo{{Name: "base.read_file"}, {Name: "context.context"}})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := bound.(*safeToolNameModel)
	inner := wrapped.inner.(*fakeToolModel)
	for _, tool := range inner.tools {
		if strings.Contains(tool.Name, ".") {
			t.Fatalf("remote tool name still contains dot: %q", tool.Name)
		}
	}

	msg, err := wrapped.Generate(context.Background(), []*schema.Message{{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call_0", Type: "function", Function: schema.FunctionCall{Name: "base.read_file", Arguments: `{}`}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := inner.seen[0].ToolCalls[0].Function.Name; got != "base_read_file" {
		t.Fatalf("request tool name = %q, want alias", got)
	}
	if got := msg.ToolCalls[0].Function.Name; got != "base.read_file" {
		t.Fatalf("response tool name = %q, want original", got)
	}
}

func TestSafeToolNameModelCoversNonBaseToolNames(t *testing.T) {
	infos := []*schema.ToolInfo{{Name: "skill.skill"}, {Name: "context.context"}, {Name: "mcp.demo.lookup"}}
	aliased, toRemote, fromRemote, err := aliasToolInfos(infos)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"skill.skill":     "skill_skill",
		"context.context": "context_context",
		"mcp.demo.lookup": "mcp_demo_lookup",
	}
	for _, info := range aliased {
		if strings.Contains(info.Name, ".") {
			t.Fatalf("remote tool name still contains dot: %q", info.Name)
		}
	}
	for local, remote := range want {
		if got := toRemote[local]; got != remote {
			t.Fatalf("toRemote[%q] = %q, want %q", local, got, remote)
		}
		if got := fromRemote[remote]; got != local {
			t.Fatalf("fromRemote[%q] = %q, want %q", remote, got, local)
		}
	}
}

func TestSafeToolNameModelDisambiguatesCollisions(t *testing.T) {
	_, toRemote, fromRemote, err := aliasToolInfos([]*schema.ToolInfo{{Name: "a.b"}, {Name: "a_b"}})
	if err != nil {
		t.Fatal(err)
	}
	first := toRemote["a.b"]
	second := toRemote["a_b"]
	if first == "" || second == "" || first == second {
		t.Fatalf("aliases should be non-empty and unique: %#v", toRemote)
	}
	if fromRemote[first] != "a.b" || fromRemote[second] != "a_b" {
		t.Fatalf("reverse aliases not preserved: %#v", fromRemote)
	}
}

func TestSafeToolNameModelDisambiguatesHashAliasCollisions(t *testing.T) {
	_, toRemote, fromRemote, err := aliasToolInfos([]*schema.ToolInfo{{Name: "a.b"}, {Name: "a_b"}, {Name: "a_b_1ba46871"}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for local, remote := range toRemote {
		if previous := seen[remote]; previous != "" {
			t.Fatalf("remote alias %q reused by %q and %q: %#v", remote, previous, local, toRemote)
		}
		seen[remote] = local
		if fromRemote[remote] != local {
			t.Fatalf("reverse alias %q = %q, want %q", remote, fromRemote[remote], local)
		}
	}
}

func TestSafeToolNameModelRestoresStreamToolCalls(t *testing.T) {
	base := &fakeToolModel{}
	bound, err := withSafeToolNames(base).WithTools([]*schema.ToolInfo{{Name: "base.read_file"}})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := bound.(*safeToolNameModel)
	inner := wrapped.inner.(*fakeToolModel)

	reader, err := wrapped.Stream(context.Background(), []*schema.Message{{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call_0", Type: "function", Function: schema.FunctionCall{Name: "base.read_file", Arguments: `{}`}}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if got := inner.seen[0].ToolCalls[0].Function.Name; got != "base_read_file" {
		t.Fatalf("stream request tool name = %q, want alias", got)
	}
	msg, err := reader.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.ToolCalls[0].Function.Name; got != "base.read_file" {
		t.Fatalf("stream tool name = %q, want original", got)
	}
	if _, err := reader.Recv(); err != io.EOF {
		t.Fatalf("second Recv err = %v, want EOF", err)
	}
}
