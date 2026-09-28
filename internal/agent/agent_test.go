// 功能：验证 Agent ReAct 主循环在工具失败、重试和最终回答前的消息写回语义。
// 调用方：go test ./internal/agent；使用内存 fake model 和临时目录工具，不访问真实 provider。
// 全局状态：无；每个测试用例隔离 HOME 和 workspace。
package agent

import (
	"context"
	"strings"
	"testing"

	agentctx "github.com/Ozqi/walle/internal/context"
	"github.com/Ozqi/walle/internal/tools"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type scriptedModel struct {
	generate []*schema.Message // 非流式路径每轮返回的消息序列
	stream   []*schema.Message // 流式路径每轮返回的消息序列
}

func (m *scriptedModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *scriptedModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if len(m.generate) == 0 {
		return &schema.Message{Role: schema.Assistant, Content: "done"}, nil
	}
	msg := m.generate[0]
	m.generate = m.generate[1:]
	return msg, nil
}

func (m *scriptedModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if len(m.stream) == 0 {
		return schema.StreamReaderFromArray([]*schema.Message{{Role: schema.Assistant, Content: "done"}}), nil
	}
	msg := m.stream[0]
	m.stream = m.stream[1:]
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func TestRunStreamMarksRecoveredToolResult(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	writeTool, err := tools.NewWriteFileTool(workspace)
	if err != nil {
		t.Fatal(err)
	}
	model := &scriptedModel{stream: []*schema.Message{
		toolCallMsg("call_fail", `{"path":"retry.txt"}`),
		toolCallMsg("call_ok", `{"path":"retry.txt","content":"ok\n"}`),
		{Role: schema.Assistant, Content: "done"},
	}}
	ag, err := NewAgent(model, []tool.BaseTool{writeTool}, &Config{Name: "test", SystemPrompt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	msgCtx, err := agentctx.NewManager().CreateContext("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.RunStream(context.Background(), msgCtx, "write file", nil); err != nil {
		t.Fatal(err)
	}

	messages, err := ag.ctxManager.GetMessages(msgCtx)
	if err != nil {
		t.Fatal(err)
	}
	assertRecoveredToolMessage(t, messages)
}

func TestRunGenerateMarksRecoveredToolResult(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	writeTool, err := tools.NewWriteFileTool(workspace)
	if err != nil {
		t.Fatal(err)
	}
	model := &scriptedModel{generate: []*schema.Message{
		toolCallMsg("call_fail", `{"path":"retry.txt"}`),
		toolCallMsg("call_ok", `{"path":"retry.txt","content":"ok\n"}`),
		{Role: schema.Assistant, Content: "done"},
	}}
	ag, err := NewAgent(model, []tool.BaseTool{writeTool}, &Config{Name: "test", SystemPrompt: "test", DisableStream: true})
	if err != nil {
		t.Fatal(err)
	}
	msgCtx, err := agentctx.NewManager().CreateContext("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.RunStream(context.Background(), msgCtx, "write file", nil); err != nil {
		t.Fatal(err)
	}

	messages, err := ag.ctxManager.GetMessages(msgCtx)
	if err != nil {
		t.Fatal(err)
	}
	assertRecoveredToolMessage(t, messages)
}

func TestRunStreamAddsStrictFinalOutputReminder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	model := &scriptedModel{stream: []*schema.Message{{Role: schema.Assistant, Content: "path ok"}}}
	ag, err := NewAgent(model, nil, &Config{Name: "test", SystemPrompt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	msgCtx, err := agentctx.NewManager().CreateContext("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.RunStream(context.Background(), msgCtx, "写完后只回复路径和验证结果", nil); err != nil {
		t.Fatal(err)
	}

	messages, err := ag.ctxManager.GetMessages(msgCtx)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range messages {
		if msg.Role == schema.System && strings.Contains(msg.Content, "最终回答只能包含用户指定的字段或内容") {
			return
		}
	}
	t.Fatalf("strict final output reminder missing from messages: %#v", messages)
}

func toolCallMsg(id string, args string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: id, Type: "function", Function: schema.FunctionCall{Name: "base.write_file", Arguments: args}}}}
}

func assertRecoveredToolMessage(t *testing.T, messages []*schema.Message) {
	t.Helper()
	for _, msg := range messages {
		if msg.Role == schema.Tool && strings.Contains(msg.Content, "Tool retry succeeded after a previous base.write_file failure") && strings.Contains(msg.Content, "must not claim the tool never failed") {
			return
		}
	}
	t.Fatalf("recovered tool result reminder missing from messages: %#v", messages)
}
