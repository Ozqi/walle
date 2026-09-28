# Slash Commands

> 当前实现见 `internal/commands/*.go`、`internal/tui/commands.go` 与 `internal/daemon/session.go`。
> 覆盖范围：当前可用 slash command、调用边界与副作用。

## 职责

`internal/commands` 保存可复用的命令处理；当前由 daemon session 调用，TUI 只把 slash 文本转发到 daemon。

## 文件

| 文件 | 命令 |
| --- | --- |
| [skill.go](../../internal/commands/skill.go) | `/skill` |
| [compress.go](../../internal/commands/compress.go) | `/compress` |
| [mcp.go](../../internal/commands/mcp.go) | `/mcp` |

## daemon session 内部命令

这些不在 `internal/commands`：

- `/session`：读取当前会话信息或操作 `context.Manager`。
- `/provider`：选择 Provider 或启动认证。
- `/model`：调用 `runtime.SwitchModel`。
- `/stop`：取消当前 Agent run context。
- `/detach`：断开当前 TUI，不停止 daemon Agent。

## 边界

- Slash 命令不经过 LLM。
- 命令错误以 system entry 显示。
- 任务管理命令由 Skill、MCP 或外置动态工具按需提供，不固定进 Runtime。
