# Daemon

`internal/daemon` 是用户级交互层，通过 `~/.walle/run/supervisor.sock` 托管多个 workspace Runtime。

## 文件

- `control.go`：Unix Socket + NDJSON server/client。
- `http.go`：显式开启的 HTTP JSON / WebSocket / SSE transport。
- `security.go`：HTTP token、Origin 和 loopback 监听校验。
- `registry.go`：创建、续接、列出和关闭 Runtime。
- `session.go`：输入、slash command、事件历史和 stop。
- `provider.go`：provider/model 列表与 OAuth。

## 交互

```text
walle -> open -> attached -> history -> ready -> live events
walle -c -> reuse latest idle Runtime or continue latest session
walle attach <id> -> attach existing Runtime
```

`detach` 只断开客户端，`stop` 才取消执行。daemon 退出后 Runtime 和事件历史消失，message session 仍保存在 `~/.walle/sessions/*.jsonl`。

显式执行 `walle daemon --http` 时会开启 localhost HTTP Gateway：

```text
GET  /v1/processes
POST /v1/runtimes/open
GET  /v1/runtimes/{id}
POST /v1/runtimes/{id}/input
POST /v1/runtimes/{id}/stop
GET  /v1/runtimes/{id}/events
GET  /v1/runtimes/{id}/attach
```

HTTP 请求必须携带 bearer/query token；token 默认保存在 `~/.walle/run/http_token`。SSE 只读，输入和停止仍通过 HTTP POST；WebSocket 是双向交互流。

当前没有通用 process 调度器、browser UI、report 或 worklog。
