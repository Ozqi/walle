# 5hAgent Learning Web Agent Prompt

你现在要从零学习 `/Users/bytedance/Proj/5hAgent` 项目，并最终产出一份“图文并茂的学习网页”，面向同样零基础的读者，解释这个项目如何从最小 Agent 一步步演进到公开 baseline。

## 工作目标

请按照 learning 分支顺序阅读项目，不要只看当前分支。你要把每个阶段学到的内容整理成一个网页，网页应当让读者明白：

1. 这个阶段解决了什么问题。
2. 相比前一阶段新增了什么能力。
3. 关键概念是什么，用浅显语言解释。
4. 关键代码模块在哪里。
5. 这个阶段为什么自然导向下一阶段。
6. 整个项目最终如何从最小 ReAct Agent 演进成更完整的 Coding Agent runtime。

最终输出应是一份可浏览的学习网页，可以是单页 HTML、Docusaurus/Markdown 页面、或项目已有前端框架下的页面。你需要根据仓库实际结构自行选择最合适的形式。

## 必须按顺序学习的分支

请依次切换并阅读这些分支：

```bash
git checkout learn/stage-1-core-agent
git checkout learn/stage-2-tools-task
git checkout learn/stage-3-skill-prompt
git checkout learn/stage-4-mcp-session-tui
git checkout learn/stage-5-current
```

不要跳读。每个分支都代表一个阶段快照。

每个阶段优先阅读：

```text
doc/0README.md
README.md
doc/
cmd/
internal/
```

其中 `doc/0README.md` 是该阶段的学习入口，先读它，再顺着它推荐的顺序读其他文档和代码。

## 阶段理解方向

### Stage 1: `learn/stage-1-core-agent`

主题：最小可运行 Agent。

你需要讲清楚：

- 什么是 Agent。
- 什么是 ReAct 循环。
- 为什么 Agent 不只是一次 LLM 调用。
- `LLM + Tool + Context` 如何组成最小闭环。
- `read_file` 和 `exec_shell` 这类工具为什么重要。

建议配图：

- 一张最小 ReAct 循环图。
- 一张 Stage 1 模块结构图。
- 一段“用户输入 -> LLM -> 工具 -> 工具结果 -> LLM”的流程图。

### Stage 2: `learn/stage-2-tools-task`

主题：流式输出、更多工具、上下文管理。

你需要讲清楚：

- 为什么模型输出要 streaming。
- 工具变多以后为什么要分类。
- 只读工具为什么可以并发，写工具为什么要谨慎。
- 上下文为什么会变长，为什么需要压缩。
- 项目如何从“能跑”走向“更像 Coding Agent”。

建议配图：

- 流式输出和普通输出的对比图。
- 工具分类图：只读工具、写工具、执行工具。
- 上下文压缩前后的示意图。

### Stage 3: `learn/stage-3-skill-prompt`

主题：Prompt 文件化和 Skill 机制。

你需要讲清楚：

- Prompt 是什么。
- 为什么 prompt 不应该一直硬编码在代码里。
- Skill 是什么，可以把它解释成“可复用的行为包”。
- frontmatter 是什么。
- Task、Command、Tool、Skill 之间的区别。
- 项目为什么开始从“加功能”转向“组织能力”。

建议配图：

- Prompt 从代码迁移到文件的示意图。
- Skill 注入到 Agent 主循环的流程图。
- `Prompt / Skill / Tool / Task` 的关系图。

### Stage 4: `learn/stage-4-mcp-session-tui`

主题：TUI、Session、MCP 和更完整的使用体验。

你需要讲清楚：

- 为什么需要 TUI。
- Session 是什么，为什么要持久化。
- Slash command 和模型 tool call 有什么区别。
- MCP 是什么，先用浅显语言解释为“外部工具的标准接入口”。
- 这个阶段为什么开始像一个产品雏形，而不只是实验代码。

建议配图：

- 用户、TUI、Agent、Context、Tool 之间的交互图。
- Session 持久化示意图。
- MCP 作为外部工具接入口的示意图。

### Stage 5: `learn/stage-5-current`

主题：公开 baseline 和阶段性收束。

你需要讲清楚：

- 什么是 baseline。
- 为什么这个分支适合作为公开学习快照。
- 前四个阶段的能力如何在这里收敛。
- 哪些能力已经比较稳定，哪些仍然是阶段性方案。
- `learn/stage-5-current` 和 `develop` 的关系：前者是学习快照，后者是继续开发主线。

建议配图：

- 一张从 Stage 1 到 Stage 5 的时间线。
- 一张最终架构鸟瞰图。
- 一张“能力演进矩阵”：阶段 vs 能力。

## 网页内容要求

网页要面向零基础读者，不要写成纯技术文档。表达上请做到：

- 每个术语第一次出现时，用一句短话解释。
- 每个阶段都要有“为什么要做这个阶段”的解释。
- 每个阶段都要有“读完这一阶段你应该理解什么”。
- 不要堆代码细节，代码只作为辅助证据。
- 使用图、流程图、表格、阶段卡片、时间线帮助理解。
- 可以使用 Mermaid 图，或者生成 SVG/HTML/CSS 图示。
- 页面应有清晰导航：阶段导航、概念导航、最终总结。
- 页面应当像学习材料，不像 README 拼接。

## 推荐网页结构

建议使用下面结构：

```text
标题：5hAgent Learning Journey

1. 项目一句话介绍
2. 如何阅读这些 learning 分支
3. Stage 1: 最小 Agent
4. Stage 2: 工具和上下文开始变复杂
5. Stage 3: Prompt 与 Skill 让能力可组织
6. Stage 4: TUI、Session、MCP 让它更像真实工具
7. Stage 5: 公开 baseline
8. 阶段能力矩阵
9. 核心概念词典
10. 最终架构图
11. 下一步：从 learning 分支走向 develop
```

## 视觉要求

网页要图文并茂，但不要做成花哨宣传页。它应该像一份清晰、克制、适合工程学习的可视化教程。

建议风格：

- 清晰的阶段时间线。
- 每个阶段一块独立区域。
- 使用流程图和模块图解释结构。
- 用表格对比阶段能力。
- 颜色克制，避免过度装饰。
- 重点突出“从简单到复杂”的递进感。

## 工作约束

- 不要修改项目代码。
- 可以新增或修改学习网页相关文件。
- 如果需要切换分支，注意不要覆盖未提交改动。
- 如果仓库当前有其他 agent 正在工作，优先使用 `git worktree` 为每个阶段创建只读学习 worktree。
- 不要假设当前分支就是要学习的状态，必须按 learning 分支逐个检查。
- 不要虚构功能，所有内容必须能从对应分支的 `README.md`、`doc/0README.md`、`doc/` 和代码结构中找到依据。
- 如果发现文档与代码不一致，请在网页中写成“阶段性边界”或“当时实现状态”，不要擅自修代码。

## 推荐操作方式

为了不影响当前工作树，建议使用 worktree：

```bash
git worktree add /tmp/5hagent-stage1 learn/stage-1-core-agent
git worktree add /tmp/5hagent-stage2 learn/stage-2-tools-task
git worktree add /tmp/5hagent-stage3 learn/stage-3-skill-prompt
git worktree add /tmp/5hagent-stage4 learn/stage-4-mcp-session-tui
git worktree add /tmp/5hagent-stage5 learn/stage-5-current
```

然后分别阅读每个目录下的：

```text
README.md
doc/0README.md
doc/
cmd/
internal/
```

学习完成后，再决定网页放在哪里。

## 最终交付

请最终交付：

1. 一个可浏览的学习网页。
2. 网页文件路径。
3. 简短说明网页如何覆盖 Stage 1 到 Stage 5。
4. 如果使用了 Mermaid、图片、CSS 或前端框架，说明如何预览。
5. 列出你实际阅读过的关键文件和分支。
