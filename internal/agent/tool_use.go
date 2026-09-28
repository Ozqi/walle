package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentctx "github.com/Ozqi/walle/internal/context"
	"github.com/Ozqi/walle/internal/logger"
	"github.com/Ozqi/walle/internal/toolevent"
	"github.com/Ozqi/walle/internal/tools"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// toolCallState 工具调用收集状态
type toolCallState struct {
	tc         schema.ToolCall
	dispatched bool
}

// toolCollector 流式工具调用收集器
// 合并 LLM 分片返回的 ToolCall
type toolCollector struct {
	states map[int]*toolCallState // index -> state
}

// newToolCollector 创建收集器
func newToolCollector() *toolCollector {
	return &toolCollector{
		states: make(map[int]*toolCallState),
	}
}

// Add 添加分片，返回可执行的调用
func (c *toolCollector) Add(chunks []schema.ToolCall) []schema.ToolCall {
	if len(chunks) == 0 {
		return nil
	}

	for _, tc := range chunks {
		logger.DebugTag("COLL", "Add chunk: id=%s name=%s args=%q", tc.ID, tc.Function.Name, logger.TruncateString(tc.Function.Arguments, 100))
		c.merge(tc)
	}
	return c.extractReady()
}

// PendingRunnableCalls 返回流式阶段尚未派发的完整工具调用。
// 流结束后，无参数工具的空参数会归一化为 {}。
func (c *toolCollector) PendingRunnableCalls() []schema.ToolCall {
	return c.runnableCalls(false, true)
}

// merge 合并单个 ToolCall
func (c *toolCollector) merge(tc schema.ToolCall) {
	idx := 0
	if tc.Index != nil {
		idx = *tc.Index
	}

	state, ok := c.states[idx]
	if !ok {
		state = &toolCallState{}
		c.states[idx] = state
	}

	// 合并字段
	if state.tc.ID == "" && tc.ID != "" {
		state.tc.ID = tc.ID
	}
	if state.tc.Function.Name == "" && tc.Function.Name != "" {
		state.tc.Function.Name = tc.Function.Name
	}
	if tc.Function.Arguments != "" {
		state.tc.Function.Arguments += tc.Function.Arguments
	}

	state.tc = synthesizeToolCallID(state.tc, idx)
}

func synthesizeToolCallID(tc schema.ToolCall, idx int) schema.ToolCall {
	if tc.ID == "" && tc.Function.Name != "" {
		tc.ID = fmt.Sprintf("call_local_%d", idx)
	}
	return tc
}

// extractReady 提取已完成的调用
func (c *toolCollector) extractReady() []schema.ToolCall {
	return c.runnableCalls(false, false)
}

func (c *toolCollector) runnableCalls(includeDispatched bool, allowEmptyArguments bool) []schema.ToolCall {
	// states 是 map，返回顺序不承诺等同模型 Index；主循环按本次派发顺序保存并回填结果。
	var ready []schema.ToolCall
	for idx := range c.states {
		state := c.states[idx]
		if state.dispatched && !includeDispatched {
			continue
		}
		tc := state.tc
		// 流式阶段要等 EOF 才能区分“无参数”和“参数还没到”。
		if allowEmptyArguments && tc.Function.Arguments == "" {
			tc.Function.Arguments = "{}"
			state.tc.Function.Arguments = tc.Function.Arguments
		}
		if tc.Function.Name == "" {
			continue
		}
		if tc.Function.Arguments == "" || !isValidJSON(tc.Function.Arguments) {
			continue
		}
		state.dispatched = true
		ready = append(ready, tc)
	}
	return ready
}

// execResult 工具执行结果
type execResult struct {
	idx    int
	tc     schema.ToolCall
	result string
	err    error
}

// toolRequest 工具执行任务单元
type toolRequest struct {
	idx int
	tc  schema.ToolCall
}

func (a *Agent) executeToolWithRepeatGuard(ctx context.Context, repeatGuard *toolRepeatGuard, req toolRequest) execResult {
	if repeatGuard != nil {
		if err := repeatGuard.Check([]schema.ToolCall{req.tc}); err != nil {
			return execResult{idx: req.idx, tc: req.tc, err: err}
		}
	}
	result, execErr := a.exeToolCall(ctx, req.tc, false)
	return execResult{idx: req.idx, tc: req.tc, result: result, err: execErr}
}

func (a *Agent) runToolWorker(ctx context.Context, repeatGuard *toolRepeatGuard, toolQueue <-chan toolRequest, toolResultCh chan<- execResult) {
	// 模型流读取可与该 worker 重叠，但工具队列只有一个消费者，工具副作用之间不并发。
	// worker 退出时关闭结果通道，通知主循环所有已派发调用均已收尾。
	go func() {
		defer close(toolResultCh)
		for req := range toolQueue {
			result := a.executeToolWithRepeatGuard(ctx, repeatGuard, req)
			select {
			case toolResultCh <- result:
			case <-ctx.Done():
				return
			}
		}
	}()
}

// exeToolCall 执行单个工具调用
func (a *Agent) exeToolCall(ctx context.Context, tc schema.ToolCall, concurrent bool) (string, error) {
	if tc.Function.Name == "" {
		logger.WarnTag("TOOL", "Skipping tool call with empty name, id=%s", tc.ID)
		return "", nil
	}

	// 1. 发出开始回调和可见事件，再从 Agent 工具索引解析实例。
	runInfo := &callbacks.RunInfo{Name: tc.Function.Name}
	if a.callbacks != nil {
		a.callbacks.OnToolStart(ctx, runInfo, &tool.CallbackInput{
			ArgumentsInJSON: tc.Function.Arguments,
		})
	}

	a.printToolCall(tc.Function.Name, tc.Function.Arguments, concurrent)

	t := a.toolMap[tc.Function.Name]
	if t == nil {
		logger.WarnTag("TOOL", "Not found: %s", tc.Function.Name)
		return "", fmt.Errorf("tool not found: %s", tc.Function.Name)
	}

	// 2. 调用工具可能产生文件、进程或网络副作用，错误原样交给上层写入 tool message。
	result, err := a.invokeTool(ctx, t, tc)

	// 3. 结束回调只负责统计和日志，不改变工具返回值。
	if a.callbacks != nil {
		if err != nil {
			a.callbacks.OnToolError(ctx, runInfo, err)
		} else {
			a.callbacks.OnToolEnd(ctx, runInfo, &tool.CallbackOutput{Response: result})
		}
	}

	return result, err
}

// invokeTool 调用工具实例
func (a *Agent) invokeTool(ctx context.Context, t tool.BaseTool, tc schema.ToolCall) (string, error) {
	if enhancedInvokable, ok := t.(tool.EnhancedInvokableTool); ok {
		toolArg := &schema.ToolArgument{Text: tc.Function.Arguments}
		toolResult, err := enhancedInvokable.InvokableRun(ctx, toolArg)
		if err != nil {
			return "", err
		}
		return formatToolResult(toolResult), nil
	}

	if invokable, ok := t.(tool.InvokableTool); ok {
		return invokable.InvokableRun(ctx, tc.Function.Arguments)
	}

	return "", fmt.Errorf("tool %s is not invokable", tc.Function.Name)
}

// addToolResult 将工具执行结果写入消息上下文。
// 参数：recovered 表示本轮同名工具此前失败过，本次成功需要显式提醒模型以最新结果为准。
// 执行错误会转成带参数和修正提示的 ToolMessage 继续交给模型；返回 error 仅表示消息写入失败。
func (a *Agent) addToolResult(messageCtx *agentctx.Context, tc schema.ToolCall, result string, execErr error, recovered bool) error {
	if execErr != nil {
		logger.ErrorTag("TOOL", "Failed: %s, err=%v", tc.Function.Name, execErr)
		a.printToolError(tc.Function.Name, tc.Function.Arguments, execErr)
		errMsg := schema.ToolMessage(formatToolErr(tc, execErr), tc.ID)
		return a.ctxManager.AddMessage(messageCtx, errMsg)
	}

	logger.DebugTag("TOOL", "Success: %s", tc.Function.Name)
	logger.DebugTag("TOOL", "  result: %s", logger.TruncateString(result, 200))
	a.printToolResult(tc.Function.Name, tc.Function.Arguments, result)

	if recovered {
		result = formatRecoveredToolResult(tc, result)
	}
	return a.ctxManager.AddMessage(messageCtx, schema.ToolMessage(result, tc.ID))
}

func formatRecoveredToolResult(tc schema.ToolCall, result string) string {
	return fmt.Sprintf("Tool retry succeeded after a previous %s failure. Latest result:\n%s\nFinal answer must use this latest successful result and must not claim the tool never failed.", tc.Function.Name, result)
}

func (a *Agent) printToolCall(name string, args string, concurrent bool) {
	if a != nil && a.toolEventSink != nil {
		text := toolevent.FormatToolCall(name, args, concurrent)
		a.toolEventSink(toolevent.ToolEvent{Kind: "call", Name: name, Text: text, Args: args, Concurrent: concurrent})
		return
	}
	toolevent.PrintToolCall(name, args, concurrent)
}

func (a *Agent) printToolResult(name string, args string, result string) {
	if a != nil && a.toolEventSink != nil {
		text := toolevent.FormatToolResult(name, args, result)
		a.toolEventSink(toolevent.ToolEvent{Kind: "result", Name: name, Text: text, Args: args, Result: result})
		return
	}
	toolevent.PrintToolResult(name, args, result)
}

func (a *Agent) printToolError(name string, args string, err error) {
	if a != nil && a.toolEventSink != nil {
		text, errText := toolevent.FormatToolError(name, args, err)
		a.toolEventSink(toolevent.ToolEvent{Kind: "error", Name: name, Text: text, Args: args, Error: errText})
		return
	}
	toolevent.PrintToolError(name, args, err)
}

// formatToolErr 格式化工具执行错误
func formatToolErr(tc schema.ToolCall, execErr error) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("tool execution failed: %v", execErr))
	if strings.TrimSpace(tc.Function.Arguments) != "" {
		b.WriteString("\nTool arguments sent by model: ")
		b.WriteString(tc.Function.Arguments)
	}
	if hint := toolHint(tc); hint != "" {
		b.WriteString("\nSuggestion: ")
		b.WriteString(hint)
	}
	return b.String()
}

// toolHint 根据工具名称返回操作提示
func toolHint(tc schema.ToolCall) string {
	name := tc.Function.Name
	display := tools.DisplayName(name)
	if strings.HasPrefix(display, "base.") {
		display = strings.TrimPrefix(display, "base.")
	}
	if strings.HasPrefix(display, "skill.") {
		display = strings.TrimPrefix(display, "skill.")
	}

	switch display {
	case "read_file", "write_file", "edit", "glob", "grep", "list_dir":
		return "check the tool arguments and retry with an absolute path under the workspace"
	case "exec_shell":
		return "check the shell command, quote paths with spaces, prefer commands inside workspace"
	case "skill":
		return "use action=list or action=get with an existing skill name"
	}

	if strings.HasPrefix(name, "mcp.") {
		// MCP 错误已在 mcp_tool.go 返回具体信息，不添加通用提示干扰
		return ""
	}

	return "review the tool schema and retry with corrected arguments"
}

// formatToolResult 将 schema.ToolResult 扁平化为模型和 UI 可消费的文本。
// 非文本 part 只保留类型占位符，二进制内容不会写入消息上下文。
func formatToolResult(toolResult *schema.ToolResult) string {
	if toolResult == nil || len(toolResult.Parts) == 0 {
		return ""
	}

	var parts []string
	for _, part := range toolResult.Parts {
		switch part.Type {
		case schema.ToolPartTypeText:
			parts = append(parts, part.Text)
		case schema.ToolPartTypeImage:
			parts = append(parts, "[Image]")
		case schema.ToolPartTypeAudio:
			parts = append(parts, "[Audio]")
		case schema.ToolPartTypeVideo:
			parts = append(parts, "[Video]")
		case schema.ToolPartTypeFile:
			parts = append(parts, "[File]")
		}
	}

	return strings.Join(parts, "\n")
}

func mergeStreamingMessageExtra(current map[string]any, incoming map[string]any) map[string]any {
	if len(incoming) == 0 {
		return current
	}
	if current == nil {
		current = make(map[string]any, len(incoming))
	}
	for key, value := range incoming {
		if prev, ok := current[key].(string); ok {
			if next, ok := value.(string); ok {
				current[key] = prev + next
				continue
			}
		}
		current[key] = value
	}
	return current
}

// isValidJSON 检查字符串是否为合法 JSON
func isValidJSON(s string) bool {
	var js json.RawMessage
	return json.Unmarshal([]byte(s), &js) == nil
}
