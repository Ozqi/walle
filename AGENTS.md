# walle

本仓库回答、注释和文档使用中文；代码标识符、命令、错误文本和外部 API 名称保持原文。涉及 TUI/CLI 视觉输出时同时阅读 [DESIGN.md](DESIGN.md)。

## 项目

`walle` 是轻量 Go + Eino Agent runtime。默认 CLI 自动启动或复用用户级 daemon，为当前 workspace 创建新 Runtime 并 attach TUI；只有 `-c/--continue` 才续接最近 Runtime 或 session。

## 代码结构

- `cmd/walle`：CLI、daemon 生命周期、`ps`、`attach`。
- `internal/daemon`：Unix Socket 协议、Runtime registry、交互 session、provider/model/OAuth。
- `internal/runtime`：配置、LLM、Prompt、Context、Skill、Tools 的装配和执行。
- `runtime`：最小 public Go SDK，只提供直接执行。
- `internal/agent`：ReAct 主循环和 ToolCall 执行。
- `internal/context`：消息、session JSONL、压缩。
- `internal/tools`：本地工具和注册表。
- `internal/tui`：Bubble Tea 客户端。

依赖方向：`cmd/TUI -> daemon -> runtime -> agent`；public `runtime -> internal/runtime`。Runtime 不依赖 daemon，TUI 不持有 Runtime。

## 稳定事实

- daemon 只使用 `~/.walle/run/supervisor.sock` 的 Unix Socket + NDJSON。
- `open/list/attach/input/stop/detach` 是当前完整控制协议。
- daemon 托管多个 Runtime；默认 `walle` 新建，`-c` 才续接。
- provider picker、OAuth 和模型列表属于 daemon；public SDK 只通过 `Options.ModelRef` 指定模型。
- Runtime 启动只注册本地 base/skill 工具和 `context.context`，不启动 MCP server。
- session 写入 `~/.walle/sessions/*.jsonl`。
- 没有 TaskList、task watcher、通用 process、report/worklog、HTTP/WebSocket/SSE gateway。

## 配置

- 模型使用 `LLM_MODEL=provider/model`。
- provider 配置使用 `LLM_<PROVIDER>_*`；`FORMAT=claude|openai` 表示协议。
- CLI `--model provider/model` 只覆盖本次 Runtime。
- Prompt 位于 `~/.walle/prompt/*.md`。
- Skill 从 `~/.walle/skills/*/SKILL.md` 和项目 `.walle/skills/*/SKILL.md` 加载，项目同名覆盖全局。

## 开发规则

1. 修改代码前读取对应 `.spec/*.md`，再核对真实调用链。
2. 开发计划写入 `.TODO/`；架构事实源写入 `.spec/diagrams/`；内部决策写入 `.doc/`；`doc/` 只发布已实现能力。
3. 优先删除、内联和复用；不要为未来需求增加接口、transport、DTO 或抽象层。
4. Go 代码执行 `gofmt`，不回滚无关工作树改动。
5. 阶段收尾至少运行受影响包检查和 `go build ./cmd/walle`；TUI 行为用真实 tmux/attach 验收。
6. 默认不提交、不推送；没有人工 review/验收不执行 `git push`。
