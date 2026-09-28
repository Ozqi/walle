# Runtime 对外 Go SDK

首版已实现：`github.com/Ozqi/walle/runtime` 薄封装 `internal/runtime`，支持 `New`、`Run`、`Info`、`Close` 和事件回调。

明确不做：daemon client、in-process session、provider/OAuth、custom tools、HTTP/WebSocket/SSE、任务调度。出现真实调用需求前不扩展接口。

验收：

```bash
gofmt -w runtime/*.go internal/runtime/*.go
go test ./runtime ./internal/runtime
go build -o /tmp/walle-sdk-check ./cmd/walle
```
