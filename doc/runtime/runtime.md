# Runtime

`internal/runtime` 负责创建和执行一个 Agent：加载配置、session、LLM、Prompt、Skill 和 Tools。它不处理 daemon 协议、provider picker、OAuth、通用 process 或 report/worklog。

## 调用链

```text
runtime.New
  -> config + session
  -> LLM + prompt
  -> Agent
  -> tools.Registry + context.context
  -> model.WithTools

Runtime.RunStream -> Agent.RunStream
```

启动阶段不连接 MCP server。

## Go SDK

外部程序可 import `github.com/Ozqi/walle/runtime`：

```go
rt, err := runtime.New(ctx, runtime.Options{
    ProjectDir: "/path/to/workspace",
    ModelRef:   "provider/model",
})
if err != nil { return err }
defer rt.Close()

result, err := rt.Run(ctx, "总结这个项目", runtime.WithEventHandler(func(event runtime.Event) {
    // assistant / thinking / tool / done / error / state
}))
```

SDK 只提供 `New`、`Run`、`Info`、`Close` 和事件回调，不包含 daemon session、provider 列表或 OAuth。它启用文件和 shell 工具，不是只读沙箱。

## 持久化

- session：`~/.walle/sessions/*.jsonl`
- 用户设置：`~/.walle/settings.json`
- 工具失败记录：`~/.walle/tool-stats/failures.jsonl` 和项目 `.walle` 目录
