# Logger / ToolEvent

- `internal/logger`：文件日志、日志级别、ANSI 颜色和字符串截断。
- `internal/toolevent`：工具调用、结果、错误的结构化事件与展示文本。
- 每个 Agent 通过 `SetToolEventSink` 把工具事件交给 daemon session、SDK 回调和失败统计。

项目不维护独立 process worklog；可恢复对话只看 `~/.walle/sessions/*.jsonl`。
