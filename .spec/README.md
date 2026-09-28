# walle Spec 总览

Spec 只记录当前代码边界和最小验收；未来能力必须显式标注 planned。

## 模块

| Spec | 代码 | Archify 图源 |
| --- | --- | --- |
| [entry.md](entry.md) | `cmd/walle`、`internal/tui` | `diagrams/walle-entry.architecture.json` |
| [daemon.md](daemon.md) | `internal/daemon` | `diagrams/walle-daemon.architecture.json` |
| [runtime.md](runtime.md) | `internal/runtime`、public `runtime` | `diagrams/walle-runtime.architecture.json` |
| [agent.md](agent.md) | `internal/agent` | `diagrams/walle-agent.architecture.json` |
| [context.md](context.md) | `internal/context` | `diagrams/walle-context.architecture.json` |
| [capability.md](capability.md) | `internal/tools`、`internal/mcp`、`internal/commands`、`internal/toolevent` | `diagrams/walle-capability.architecture.json` |
| [knowledge.md](knowledge.md) | `internal/skill` | `diagrams/walle-knowledge.architecture.json` |
| [model-config.md](model-config.md) | `internal/llm`、`internal/codex`、`internal/utils`、`internal/logger` | `diagrams/walle-model-config.architecture.json` |

## 依赖方向

```text
cmd/TUI -> daemon -> runtime -> agent -> context
                          |-> tools -> skill / mcp
                          |-> llm / codex
public runtime ----------> runtime
```

- Daemon 可以依赖 Runtime；Runtime 不依赖 Daemon。
- TUI 只依赖 daemon client 和 DTO。
- Public SDK 只包装 Runtime 执行，不复制 daemon 语义。
- 默认 transport 是 Unix Socket + NDJSON；显式 `walle daemon --http` 可开启 localhost HTTP/WebSocket/SSE Gateway。
- 没有 TaskList、通用 process、report/worklog 或 watcher。

## 架构图

`.spec/diagrams/walle-architecture-topology.json` 是全局拓扑事实源；Mermaid 保留当前主要链路。

Archify 子模块图源也放在 `.spec/diagrams/`，只入库 `*.architecture.json`。生成的 HTML、截图和 visual-check 产物不入库；需要查看时用 Archify 本地生成：

```bash
node ~/.claude/skills/archify/bin/archify.mjs deliver architecture .spec/diagrams/walle-entry.architecture.json /tmp/walle-entry.html --quality showcase
```

## 修改规则

1. 先读目标 spec 和真实调用链。
2. 优先删除未使用设计，避免为未来需求增加接口。
3. Go 代码执行 `gofmt`。
4. 至少运行受影响包测试和 `go build ./cmd/walle`。
5. 改架构图时先更新对应 `*.architecture.json`，再用 Archify validate/deliver/visual-check 验证。
