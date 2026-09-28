# Runtime 对外接口

当前 public `github.com/Ozqi/walle/runtime` 已收敛为最小嵌入式入口：

- `New(ctx, Options)`
- `Run(ctx, input, WithEventHandler(...))`
- `Info()`
- `Close()`

不公开 daemon session、provider/OAuth、socket、任务调度或 custom tool registration。后续只有出现真实外部调用需求时再增加接口。
