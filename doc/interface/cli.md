# TUI

> 当前实现见 `cmd/walle/*.go`、`internal/tui/*.go` 与 `internal/daemon/*.go`。
> 覆盖范围：默认 TUI、daemon attach、slash command 与状态渲染。

## 职责

`internal/tui` 是独立 Bubble Tea 客户端包，只负责输入、渲染和事件转发。默认 `walle` 自动启动或复用 daemon，再通过 Unix Socket 为当前 workspace 打开新的交互 Runtime 并 attach；`-c/--continue` 才续接最近 Runtime 或 session。daemon 不依赖具体 TUI 实现。

## 关键文件

| 文件 | 作用 |
| --- | --- |
| [app.go](../../internal/tui/app.go) | `AppModel`、viewport、事件渲染 |
| [commands.go](../../internal/tui/commands.go) | attached 输入处理，本地只截获 `/detach` 和 `/stop` |
| [remote.go](../../internal/tui/remote.go) | daemon attach 客户端入口 |
| [markdown.go](../../internal/tui/markdown.go) | Markdown 终端渲染 |
| [ui.go](../../internal/cli/ui.go) | 非 TUI 错误输出 |

## 布局

```text
history viewport
runtime status       # model/state/turn/tools
slash hints          # 只在输入 / 时出现
input bar
footer metadata      # path/git/msg count，固定存在
bottom spacer        # 空白占位，不显示 busy spinner
```

## Slash 命令

| 命令 | 行为 |
| --- | --- |
| `/provider [name]` | 发给 daemon session；由 daemon 选择 Provider 或完成认证 |
| `/model [provider/model]` | 发给 daemon session；由 Runtime 查看或切换当前模型 |
| `/skill list|get|reload` | 发给 daemon session；由 daemon 调 `commands.HandleSkill` |
| `/compress` | 发给 daemon session；由 daemon 调 `commands.HandleCompress` |
| `/mcp` | 发给 daemon session；由 daemon 管理 MCP 配置 |
| `/session` | 发给 daemon session 查看当前 session |
| `/stop` | 通过 socket 取消当前 Agent run |
| `/detach` | 退出 attached TUI，不停止 Agent |

TUI 本地只直接处理 `/detach` 和 `/stop`；其它 slash command 都作为输入转发给 daemon session。任务管理命令不固定进 Runtime；需要时由 Skill、MCP 或外置动态工具提供。

## 快捷键

- `Ctrl+C`：清空当前输入框；短时间内第二次 `Ctrl+C` 退出 attached TUI。
- `Ctrl+D`：退出 attached TUI，不停止 daemon Agent。
- `Ctrl+U`：清空当前输入框，不触发二次退出确认。
- Agent 忙碌时按 Enter 会把当前输入排队；本轮输出结束后 TUI 自动提交下一轮。当前不做 ReAct 循环中途插入，避免破坏 tool call/result 消息顺序。

## 运行状态

- `busy/currentStatus/spinnerFrame` 驱动顶部状态行。
- TUI 的 `/stop` 只调用远端 `stop` 控制帧；真正的 run cancel func 保存在 daemon session。
- 迟到 token 在 `busy=false` 后被忽略。
- footer 显示路径但不显示 `dir` 字样。
- git 主仓库显示 `git <branch>`；linked worktree 显示 `worktree <branch>`。
- 消息时间显示在首行右侧。

## 工具事件

```text
ToolEvent(call)   -> running hint
ToolEvent(result) -> 原地更新为 done
ToolEvent(error)  -> 原地更新为 error
```

工具标题使用蓝色工具名和蓝色状态符号；error 保留红色。

## 历史恢复

attached TUI 不直接读取 session store；重连后的历史由 daemon attach 握手回放为 `ProcessEvent`，TUI 只按事件渲染。

## 验证

TUI 视觉改动以真实 tmux 画面为准。不要每个小改动都启动/重启；一组相关改动完成后，再用当前 `walle debug` 或临时 tmux session 验收。
