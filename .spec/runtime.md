# Runtime Spec

## 职责

`internal/runtime` 只装配和执行一个 Agent Runtime：配置、LLM、Prompt、Context、Skill 和 Tools。

```text
runtime.New -> config -> context -> llm -> agent -> tools
Runtime.RunStream -> Agent.RunStream
Runtime.SwitchModel -> rebuild model + rebind tools
```

## 文件

- `runtime.go`：初始化、执行、模型切换、关闭。
- `provider.go`：仅保留创建模型所需的 Codex 格式选择。
- `hooks.go`、`tool_stats.go`：工具事件 hook 和失败记录。

Daemon 交互、slash command、provider picker、OAuth 和事件回放都属于 `internal/daemon`，不得放回 Runtime。

## 接口

- `New(ctx, Options)`：创建独立 Runtime。
- `RunStream(ctx, input, callbacks)`：执行一轮普通输入。
- `SwitchModel(ctx, modelRef)`：切换当前 Runtime 模型并重新绑定工具。
- `Info()`：返回只读状态。
- `Close()`：释放资源。

根目录 `runtime/` 是最小 public Go SDK，只暴露 `New`、`Run`、`Info`、`Close` 和事件回调；不暴露 daemon session、provider 列表、OAuth、socket 或任务调度。

## 约束

- 默认 `walle` 创建新 Runtime；只有 `-c/--continue`、`--session` 或 `attach` 才续接。
- 每个 Runtime 独占 `Agent`、`MessageCtx` 和 `tools.Registry`。
- 启动阶段不连接 MCP server。
- Runtime 不包含 TaskList、watcher、通用 process、report/worklog 或 TUI 代码。
- 模型切换必须同时替换 `context.context` 并重新绑定完整工具集合。

## 验收

- `gofmt -w internal/runtime/*.go runtime/*.go`
- `go test ./internal/runtime ./runtime`
- `go build -o walle ./cmd/walle`
