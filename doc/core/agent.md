# Agent

## 职责

`internal/agent` 执行一次 ReAct 回合：注入 system/skill、追加用户消息、调用模型、收集 ToolCall、执行工具、把 assistant/tool 结果写回 context。

## 关键文件

| 文件 | 作用 |
| --- | --- |
| [agent.go](../../internal/agent/agent.go) | `Agent`、`RunStreamWithOptions`、skill 注入、重复工具防护 |
| [tool_use.go](../../internal/agent/tool_use.go) | 流式 ToolCall 合并、工具执行、结果格式化 |
| [callbacks.go](../../internal/agent/callbacks.go) | LLM/tool 调试回调 |
| [debug_request.go](../../internal/agent/debug_request.go) | `--debug` 下记录发给 LLM 的逻辑请求 |

## 主流程

```text
RunStreamWithOptions
  -> ensureConversationSetup
  -> WithToolRuntime
  -> AddMessage(user)
  -> optional strict final-output reminder
  -> optional LMCompress
  -> loop:
       GetMessages
       model.Stream / model.Generate
       collect valid ToolCall
       exeToolCall sequentially
       AddMessage(assistant with tool_calls)
       AddMessage(tool result)
       continue until assistant text without tool_calls
```

## 关键函数

| 函数 | 细节 |
| --- | --- |
| `NewAgent` | 构建 `toolMap`，加载 skill manager，设置默认 `RepeatToolLimit=5`。 |
| `RunStreamWithOptions` | 主循环；`DisableStream=true` 时走 `Generate` 兼容路径。 |
| `ensureConversationSetup` | 仅在空 context 时注入 system prompt 和已加载 skills。 |
| `strictFinalOutputRequested` | 识别“只回复/只输出”等严格最终格式要求，并在本轮模型调用前追加临时 system reminder。 |
| `toolRepeatGuard.Check` | 按 `tool name + normalized JSON args` 计数；当前超过阈值只记录 warn。 |
| `toolCollector.Add` | 合并流式 ToolCall 分片；参数 JSON 完整后才可执行。 |
| `exeToolCall` | 触发工具事件 sink，调用 Eino tool，保留参数和错误提示。 |
| `addToolResult` | 成功写 `schema.ToolMessage(result, id)`；失败写格式化错误消息；失败后成功重试会提示最终回答以最新成功结果为准。 |

## 状态边界

- `Agent.currentTurn` 只反映最近一次 ReAct loop 轮次。
- `Context` 持久化归 `internal/context.Manager`；Agent 不直接操作 session 文件。
- 工具执行当前是单 worker 顺序执行；stream 读取和工具执行可以重叠。
- `/skill reload` 可刷新 manager 快照，但不会替换当前 context 已注入的 skill message。
- `--debug` 在模型调用前记录 model、messages 和 tool call 字段；API key 固定掩码，无法展开的 model options 标记为 `unavailable`。
