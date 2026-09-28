---
name: walle-terminal-design
version: 1
updated: 2026-09-14
scope: terminal TUI, CLI visual output, diagrams that need product styling
---

# walle DESIGN.md

本文件是 walle 的视觉设计源文件。后续修改 TUI、CLI 视觉输出或带产品风格的图时，先读本文件；工程规则仍以 `AGENTS.md` 和对应 `.spec/*.md` 为准。

## 设计方向

walle 使用 WALL·E 机器人风格：温暖、旧机器、低噪声、可信赖。Claude Code 只作为终端交互密度和布局节奏参考，不复制它的品牌、文案或不存在的功能。

walle 的产品隐喻是“自主整理垃圾的小机器人”：面对混乱的仓库、TODO、上下文和运行状态，它应该安静地识别杂乱、压缩噪声、保留关键事实，把乱摊子整理得井井有条。这和项目的工程思想一致：简单、少绕路、少废话、真实状态优先。

walle 的产出应像被压缩整理后的方块：边界清楚、可堆叠、可扫描、形状稳定。长文本、工具结果、TODO 和状态摘要都应尽量被整理成规整的信息块，而不是散乱铺开。

关键词：暖黄、锈橙、机械灰、柔和蓝、少文案、真实状态、左上角启动上下文、整理感、秩序感、方块感。

## 颜色 token

只使用下面这些语义色；需要新颜色时先补本文件，再改代码。

| Token | Hex | 用途 |
| --- | --- | --- |
| `walle-yellow` | `#E7B84A` | 主提示符、当前输入、重点状态 |
| `rust-orange` | `#D9902F` | 命令、品牌字符画、主要强调 |
| `mechanic-gray` | `#5F6A61` | 分隔线、结构线、弱边框 |
| `soft-blue` | `#76A7B8` | 工具、分支、辅助高亮 |
| `moss-green` | `#9CB66F` | 成功、可用、连接正常 |
| `aged-purple` | `#A88F72` | 次级状态、滚动百分比 |
| `warm-white` | `#E8E1CF` | 主文字 |
| `paper-dim` | `#CFC6AA` | 输出正文、代码块正文 |
| `dust-muted` | `#9AA28E` | 辅助说明、hint、低优先级信息 |
| `warning-red` | `#D66A4A` | 错误、失败、停止 |
| `panel-bg` | `#3D453F` | 输入区必要背景；默认少用整块背景 |

## 终端布局

- 单列布局：历史区在上，输入/状态区固定在底部。
- 启动上下文在 scrollback 第一条，跟随历史滚动；首次进入应先可见。
- 启动上下文采用左上角轻量结构：左侧 WALL·E 字符画，右侧 2-3 行真实上下文。
- footer 只展示真实运行信息：workspace、git、model、token/context、queued、scroll。
- 分隔线使用机械灰细线，不做大面积卡片边框。
- 文案短：能一行说清，不写第二行。

## TUI 组件规则

### Prompt

- 输入提示符使用 `❯`。
- prompt 使用 `walle-yellow` 加粗。
- 普通输入文字使用 `warm-white`。

### 启动上下文

必须展示：

- WALL·E 字符画。
- 预载 prompt 文件名。
- Skill 标题。

不得展示：

- 已在 footer 里的 model、workspace、session 重复信息。
- walle 不存在的能力。
- 模仿 Claude Code 的产品名或品牌文案。

### Tool / assistant / system

- assistant 主标记使用 `⏺`。
- tool call/result 采用紧凑两行结构，参数放括号。
- thinking 使用低噪声标记，不刷屏。
- error 使用 `warning-red` 和 `✘`。
- system/recap 使用 `※`，保持弱提示。

### Footer 与 hint

- hint 只写用户当前可用且值得提示的快捷键。
- 不提示常识性或未完整实现的能力，例如只有最近一次输入恢复时，不写完整 history 提示。
- 禁止展示 `auto mode`、`agents` 等 walle 当前没有的功能。

## 密度与间距

- 默认紧凑，避免大段空白。
- 重要块之间最多 1 个空行。
- 窄屏优先保留信息真实性，必要时折叠为纵向布局。
- 不用装饰性阴影、渐变、大面积色块。

## Do / Don't

Do:

- 使用本文件 token 对齐配色。
- 先复用现有 TUI helper 和样式变量。
- 用真实 tmux capture 验证视觉变化。
- 保持 WALL·E 主题和 Claude Code 式终端节奏的平衡。

Don't:

- 不新增不存在的功能提示。
- 不把 Claude Code 的品牌、状态文案或功能照搬进 walle。
- 不为了“好看”重复 footer 已有信息。
- 不引入复杂主题系统；当前只需要一套 WALL·E terminal 主题。
