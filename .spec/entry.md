# Entry / TUI Spec

## 职责

入口层只解析命令、连接 daemon 和渲染事件。

- `walle`：open 当前 workspace 的新 Runtime。
- `walle -c`：显式续接最近 idle Runtime；不存在时续接最近 session 创建。
- `walle ps`：列出 daemon 中的 Runtime。
- `walle attach <id>`：连接已有 Runtime。
- `walle daemon`：启动用户级 Unix Socket daemon；显式 `--http` 时额外开启 localhost HTTP/WebSocket/SSE。

```text
cmd/walle -> internal/daemon.ProcessClient -> internal/tui
walle daemon -> internal/daemon.Registry -> internal/runtime
```

## 文件

- `main.go`：Cobra 和 daemon 生命周期。
- `interactive_command.go`：自动启动 daemon，发送 open。
- `process_commands.go`：ps、attach。
- `internal/tui`：Bubble Tea 输入和渲染。

## 边界

- TUI 本地只处理 `/detach` 和 `/stop`；其他 slash command 发给 daemon session。
- TUI 不读取 provider 配置、不发起 OAuth、不持有 Runtime。
- 默认入口不得静默续接；只有 `-c`、`--session`、`attach` 可以续接。
- 默认只有 Unix Socket + NDJSON；HTTP/WebSocket/SSE 只在显式 `walle daemon --http` 时开启。
- 不恢复 `walle run`、固定 `/task`、`daemon --poll` 或 `daemon --interactive`。

## 验收

- open、history、ready、live event 顺序正确。
- stop 取消当前执行；detach 只断开客户端。
- 同 workspace 默认新建，`-c` 才续接。
