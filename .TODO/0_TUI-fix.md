# TUI相关TODO

## TUI 表现验收 SOP

目标：每次修 TUI 体验时，用真实 tmux 画面做最小验收，优先发现输入、滚动、刷新和布局问题。

1. 选择可见窗口：优先使用当前 `tmux` 会话里的 `walle:6.1`；如果不存在，再新建临时窗口。
2. 启动方式：在 `/Users/bytedance/Proj/5hWorkSpace` 跑 `walle`，或在已存在 attached TUI 中直接复测。记录 pane、尺寸、命令。
3. 基线截图：执行 `tmux capture-pane -t '<pane>' -p -S -80`，确认底部输入框、状态栏、模型名、git/workspace 信息可读。
4. 输入验收：输入 `/`、Tab、`/model`、`/skill list`，确认 slash hint、picker、系统输出和输入框状态符合预期。
5. 滚轮验收：在空输入框和非空输入框各滚动几次，再 capture；验收标准是输入框不出现 `[<64;...M` / `[<65;...M`。
6. 长历史验收：让页面包含 tool 输出或长 Markdown，按 `PgUp/PgDown/Home/End`，确认 scroll 百分比变化、底部输入框固定、边框不换行。
7. 忙碌验收：发一个会触发 thinking/tool 的请求，观察 5-10 秒；验收标准是 spinner 不造成明显卡顿，用户仍能输入 `/stop`，状态栏不刷屏错位。
8. 窄屏验收：用临时 tmux pane/window 跑一次 `100x28` 或 `80x24` capture；验收标准是 slash hint、工具行和 footer 保持可读。
9. 收口记录：把复现命令、capture 现象、修复目标写到本文件。

推荐命令：

```bash
tmux list-panes -a -F '#{session_name}:#{window_index}.#{pane_index}:#{window_name}:#{pane_width}x#{pane_height}:#{pane_current_path}:#{pane_current_command}'
tmux capture-pane -t 'walle:6.1' -p -S -80
tmux send-keys -t 'walle:6.1' C-u '/'
tmux send-keys -t 'walle:6.1' Escape '[<64;52;29M' Escape '[<65;52;30M'
```

## 2026-08-21 tmux 真实使用发现

复现环境：`tmux` 会话 `walle`，窗口 `6:walle`，pane `walle:6.1`，尺寸 `215x48`，当前 attached daemon workspace 为 `/Users/bytedance/Proj/walle`。

- [x] P0：鼠标滚轮事件会漏进输入框，表现为输入框出现 `[<64;52;29M`、`[<65;52;30M` 等 SGR mouse escape 片段。复现：在 TUI 空输入状态滚动历史区，`tmux capture-pane -t 'walle:6.1' -p -S -40` 可见输入行从 `>` 变成 `> [<64;52;29M[<64;52;29M...`。修复目标：滚轮只滚动 viewport，不修改 textarea 内容；即使终端把 `Escape` 和后续字节拆成多条 `tea.KeyMsg`，也要完整吞掉该序列。2026-08-24 已在 `Update` key 入口、textarea 更新后和 submit 前增加 SGR mouse escape 清理；`walle-ui-check` 实测发送 `[<64;10;10M` / `[<65;10;10M` 后输入框未出现 escape 串。
- [x] P0：TUI streaming/thinking 时 spinner 刷新频率过高，界面明显卡顿，尤其在长历史、长 tool 输出和大终端宽度下更明显。复现：提交普通问题后状态栏持续显示 `⠋/⠙/⠹ thinking`，历史区含大量工具输出时刷新压力很高。修复目标：降低 spinner tick 频率，或只在状态变化/新 token/tool event 时刷新；保持用户输入和滚动响应流畅。2026-08-24 已增加 spinner 单定时链、token 渲染节流和 entry 渲染缓存，spinner 间隔从 120ms 降为 180ms。
- [x] P1：attached daemon 模式下输入 `/model` 后状态会变为 `submitted` / `thinking`，用户没有立即看到 model picker 或 usage，像普通 LLM 请求一样进入忙碌状态。复现：在 `walle:6.1` 输入 `/model` 后，状态栏短时间显示 `submitted`，随后仍进入 thinking/工具调用历史上下文。修复目标：远端 slash 命令应在本地保持 command 状态，picker/system 事件到达后恢复 idle；`/model`、`/provider` 等本地命令不应触发普通对话轮次的 busy 表现。2026-08-24 已把 attached submit/stop 改成异步 Cmd，slash command 显示 command 状态且不设置 busy。
- [x] P1：attached TUI 底部 workspace/git 状态取的是客户端当前目录 `/Users/bytedance/Proj/walle`，而命令入口可能来自 `/Users/bytedance/Proj/5hWorkSpace`，`walle ps` 也按 workspace 过滤，容易让用户误判当前连到哪个项目。修复目标：attached 模式优先展示 daemon snapshot 的 `Workspace`，并明确本地 client cwd 与远端 runtime workspace 的关系。2026-08-24 已让 attached TUI 以 daemon snapshot workspace 初始化 footer，并把 git 元信息加载移出 View 同步路径。

## 从旧记录收拢的 TUI 待修复

这些项和 TUI 真实使用体验相关，后续统一在本文件跟踪。

- [ ] P1：输入历史目前只有 `↑` 恢复最近一次提交，尚未实现多条历史上下翻。修复目标：按需增加完整输入历史栈；未实现前底部提示不展示该能力。
- [ ] P1：失败工具行虽然已表格化，但复杂任务下仍需验证长命令、长路径、长错误输出不会导致换行错乱、截断不清或状态栏错位。修复目标：工具行在常见宽度和窄屏下可读，失败原因能一眼定位。
- [ ] P1：复杂长任务在普通对话模式下连续工具调用约 8 次后可能停下并请求“允许继续推进”，产物尚未写完。修复目标：TUI 清楚展示当前执行是否结束；持续执行能力由后续显式 process/调度接口承载。
- [ ] P1：`write_file` 失败后二次修正可以成功，但最终报告可能保留“工具未失败”等过期事实。修复目标：工具失败、重试和修正结果在 TUI 历史与最终回答中保持一致，避免旧错误结论残留。
- [ ] P1：读文件或 grep 后本地模型可能空响应终止。修复目标：TUI 能识别空 assistant 响应，给出可继续、可重试或可诊断的状态提示。
- [ ] P2：最终回复可能超过用户要求的“只回复路径和验证结果”。修复目标：TUI/process 的收尾提示能尊重用户显式输出约束，必要时把详细信息放报告或日志。
- [ ] P2：新启动 `walle --session ...` 曾多次被系统直接 `killed`。修复目标：保留最小诊断入口，能区分 Ollama/本地模型内存压力、会话加载过大和启动期资源峰值。
