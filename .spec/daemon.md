# Daemon Spec

## 职责

`internal/daemon` 是用户级交互层，核心责任：

1. 默认用 Unix Socket + NDJSON 接收 `open/list/attach/input/stop/detach`。
2. 显式 `walle daemon --http` 时额外开启 localhost HTTP/WebSocket/SSE transport。
3. 托管多个 workspace Runtime，并维护 attach 所需的事件历史。
4. 处理交互命令，包括 provider/model 列表、OAuth 和模型切换。

```text
CLI/TUI <-> supervisor.sock <-> internal/daemon/control.go <-> DaemonSession <-> internal/runtime
Browser/IDE/Script <-> HTTP/WS/SSE <-> internal/daemon/http.go <-> DaemonSession <-> internal/runtime
```

不再保留独立 Agentd 调度核心、通用 process、report 或 worklog。HTTP/WebSocket/SSE 只作为 daemon 的可选 transport，不引入第二套 Runtime 生命周期。

## 文件

- `control.go`：协议 DTO、Unix Socket server/client。
- `http.go`：显式开启的 HTTP JSON / WebSocket / SSE transport。
- `security.go`：HTTP token、Origin 和 loopback 监听校验。
- `session.go`：单 Runtime 的输入、slash command、事件历史、订阅和 stop。
- `provider.go`：provider/model 列表与 OAuth。
- `cmd/walle/main.go`：Runtime registry 和 daemon 生命周期。

## 协议

- `open`：创建或显式续接 workspace Runtime，并立即 attach。
- `list`：列出 daemon 中的 Runtime。
- `attach`：连接已有 Runtime。
- `input`：提交输入。
- `stop`：取消当前执行。
- `detach`：只断开客户端。
- Attach 顺序固定为 `attached -> history events -> ready -> live events`。

HTTP Gateway 路由：

```text
GET  /v1/processes
POST /v1/runtimes/open
GET  /v1/runtimes/{id}
POST /v1/runtimes/{id}/input
POST /v1/runtimes/{id}/stop
GET  /v1/runtimes/{id}/events       # SSE readonly stream
GET  /v1/runtimes/{id}/attach       # WebSocket interactive stream
```

## 边界

- 默认 `walle` 创建新 Runtime；`-c/--continue` 才复用最近 idle Runtime 或 session。
- DaemonSession 可以依赖 Runtime；Runtime 不依赖 daemon。
- TUI 只依赖 daemon DTO 和 client，不读取配置、不调用 Runtime。
- provider/OAuth 属于 daemon 交互，不进入 public Runtime SDK。
- HTTP Gateway 默认关闭；只有显式 `--http` 才监听 localhost。
- HTTP/WebSocket/SSE 请求必须通过 token 校验；浏览器请求还要通过 Origin 校验。
- 默认不允许非 loopback 监听；必须显式 `--allow-non-loopback`。

## 验收

- 同一 workspace 连续 `walle` 得到不同 Runtime。
- `walle -c` 复用最近 idle Runtime。
- `ps`、`attach`、`stop`、`detach` 工作。
- history replay 与 live event 无缺口。
