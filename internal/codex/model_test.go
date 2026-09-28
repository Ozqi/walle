// 功能：验证 Codex Responses 适配器的工具名映射和 SSE 事件解析，不触发真实 OAuth。
// 调用方：go test ./internal/codex；使用内存 reader/writer，不访问 ChatGPT 或用户凭据。
// 全局状态：无。
package codex

import (
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestCodexModelAliasesToolsInRequest(t *testing.T) {
	bound, err := (&Model{name: "gpt-test"}).WithTools([]*schema.ToolInfo{{Name: "base.read_file"}, {Name: "context.context"}})
	if err != nil {
		t.Fatal(err)
	}
	m := bound.(*Model)
	body, err := m.buildRequest([]*schema.Message{{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call-1", Function: schema.FunctionCall{Name: "base.read_file", Arguments: `{}`}}}}})
	if err != nil {
		t.Fatal(err)
	}
	input := body["input"].([]any)
	call := input[0].(map[string]any)
	if call["name"] != "base__read_file" {
		t.Fatalf("request tool name = %#v, want base__read_file", call["name"])
	}
	tools := body["tools"].([]any)
	first := tools[0].(map[string]any)
	if first["name"] != "base__read_file" {
		t.Fatalf("schema tool name = %#v, want base__read_file", first["name"])
	}
}

func TestCodexReadSSERestoresToolCallsAndUsage(t *testing.T) {
	m := &Model{name: "gpt-test", fromRemote: map[string]string{"base__read_file": "base.read_file"}}
	reader, writer := schema.Pipe[*schema.Message](8)
	body := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hi"}`,
		``,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call-1","name":"base__read_file","arguments":"{}"}}`,
		``,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`,
		``,
		``,
	}, "\n"))
	if err := m.readSSE(body, writer); err != nil {
		t.Fatal(err)
	}
	writer.Close()

	msg, err := reader.Recv()
	if err != nil || msg.Content != "hi" {
		t.Fatalf("first SSE msg = %#v, err=%v; want text delta", msg, err)
	}
	msg, err = reader.Recv()
	if err != nil || len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "base.read_file" {
		t.Fatalf("tool SSE msg = %#v, err=%v; want restored tool call", msg, err)
	}
	msg, err = reader.Recv()
	if err != nil || msg.ResponseMeta == nil || msg.ResponseMeta.Usage.TotalTokens != 5 {
		t.Fatalf("final SSE msg = %#v, err=%v; want usage meta", msg, err)
	}
	if _, err := reader.Recv(); err != io.EOF {
		t.Fatalf("Recv after close err = %v, want EOF", err)
	}
}

func TestCodexBuildRequestToolChoice(t *testing.T) {
	m := &Model{name: "gpt-test"}
	choice := schema.ToolChoiceForbidden
	body, err := m.buildRequest(nil, model.WithToolChoice(choice))
	if err != nil {
		t.Fatal(err)
	}
	if body["tool_choice"] != "none" {
		t.Fatalf("tool_choice = %#v, want none", body["tool_choice"])
	}
}

func TestCodexReadSSERejectsIncompleteStream(t *testing.T) {
	reader, writer := schema.Pipe[*schema.Message](1)
	defer reader.Close()
	if err := (&Model{}).readSSE(strings.NewReader(`data: {"type":"response.incomplete"}

`), writer); err == nil {
		t.Fatal("readSSE incomplete event succeeded, want error")
	}
}
