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

> 2026-09-15 收口口径：用户在 Stop hook 后先选择“只补压力项”，随后明确选择“修订目标”。本轮目标收口为“已授权范围内的工具/功能与压力项测试完成”。provider 外部登录、全 provider/model 矩阵、用户级 `~/.walle/mcp.json` 到 Runtime 的真实 MCP 集成链路不属于当前授权范围：不触发 provider 登录、不跑全 provider/model 矩阵、不改用户级 MCP 配置链路。
>
> 2026-09-15 授权范围结论：按用户选择的修订目标，已授权范围内的工具/功能与压力项测试已收口；原始字面“所有工具/所有功能”目标因未授权项存在而不再作为本轮关闭条件。
>
> 2026-09-15 当前压力项覆盖：窄屏长错误、窄屏长 footer metadata 不溢出、busy queue + `ctrl+c` 草稿清空、双 `ctrl+c` 退出不丢 pending queue、queued user echo replay 去重、daemon `done/error -> state idle` 终态保持、done/error/stop 后自动提交 pending queue 并立即显示等待占位、queued submit ack 不重复追加等待占位、遇到 `agent is busy` 时保留队列、运行中 slash/picker submit 成功 ack、submit error 或 system `agent is busy` 时都不终止当前 run、daemon disconnect 清空等待占位、去重且断开后 submit 阻断、picker Enter 异步提交保持 command/非 busy、运行中 model event 只更新名称不终止 run、运行中 picker event/esc/ctrl+c 保留 busy、运行中 stop、stop 失败清理等待占位并显示 error、运行中普通 system/submit error 不误停当前 run、断连后保留 disconnected/queued 可见状态并阻断迟到 event/submit/stop/tool/done/error/thinking/token、输入、/stop 和 pending 自动提交，断连清理 spinner/intro blink/live counters/tool counters/user echo/assistant 指针/退出确认态并关闭 transient picker、busy ctrl+c/ctrl+d 不丢 pending queue、运行中 detach/reattach、多工具连续调用。`internal/mcp` 已用测试二进制覆盖真实 stdio 子进程 initialize、tools/list、tools/call；默认 Runtime 读取用户 MCP 配置的端到端验收保留为未授权范围。

- [x] P1：输入历史原先只有 `↑` 恢复最近一次提交。已增加最小输入历史栈：提交时记录非空输入，`↑` 向旧记录浏览，`↓` 向新记录浏览并回到进入浏览前的草稿；用 TUI 状态机单测覆盖。
- [x] P1：失败工具行虽然已表格化，但复杂任务下仍需验证长命令、长路径、长错误输出不会导致换行错乱、截断不清或状态栏错位。已新增长路径/长错误工具行渲染单测，确认 80 列宽下每行不溢出，并保留工具名、关键失败原因和截断标记。
- [x] P1：复杂长任务在普通对话模式下连续工具调用约 8 次后可能停下并请求“允许继续推进”，产物尚未写完。已让 assistant done 后 footer 保留 `done` 终态，而不是立即回到 `idle`；忙碌中仍显示带时长状态，完成后清楚表达当前执行已结束。2026-09-15 追加覆盖 daemon `done -> state idle` 组合，避免完成态被随后空闲 state 覆盖。持续执行能力仍由后续显式 process/调度接口承载。
- [x] P1：`write_file` 失败后二次修正可以成功，但最终报告可能保留“工具未失败”等过期事实。已在 Agent 工具结果写回中记录本轮曾失败的工具；同名工具后续成功时，成功 tool message 会带上“retry succeeded / must not claim the tool never failed”提示，驱动最终回答以最新成功结果为准；流式和非流式路径均有单测覆盖。
- [x] P1：读文件或 grep 后本地模型可能空响应终止。已在 TUI 运行状态中记录当前轮是否收到非空 assistant token；done 时若仍无输出，会清理等待占位并追加 `assistant returned empty response` 系统提示，用状态机单测覆盖空响应和非空响应两条路径。
- [x] P1：2026-09-15 复测 3 号窗口工具调用时，`read_file` 的 tool args 可能从模型侧传来 JSON `null`，TUI 会显示成 `read_file(null)`。已在 `formatToolArgsSummary` 中把 `null` 当作无参数压掉，并用单测覆盖。
- [x] P1：2026-09-15 复测 3 号窗口 reattach 时，历史里的 `/provider` picker 会被恢复成当前模态，导致重连后卡在旧选择器。已在 daemon client replay 阶段跳过 transient picker event，只保留 live picker。
- [x] P1：2026-09-15 `/stop` 停止仍在等待首 token 的运行后，历史里会残留空 assistant `⏺`。已在 system event 终止路径清理 waiting placeholder，并用 TUI 状态机单测覆盖。
- [x] P1：2026-09-15 在 traexCLIproxy/gpt-5.5 rate limit 期间，`list_dir`、`glob`、`exec_shell` 工具结果已正常显示，但工具结果后的模型收尾请求多次 502；切到 `traexCLIproxy/deepseek-v4-flash` 后完成剩余真实矩阵，确认远端 502 不是 TUI 展示 bug。
- [x] P1：2026-09-15 3 号窗口真实完成 base tool 矩阵：`list_dir`、`glob`、`exec_shell`、`read_file`、`read_md`、`grep`、`write_file`、`edit`。其中 `read_md` 已从压缩 JSON 改为 `total lines/headings/L行号 H级别` 摘要，`write_file` 已改为 `bytes/message` 摘要，`edit` diff 展示正常。
- [x] P1：2026-09-15 3 号窗口真实验证 busy 输入 queue：第一条 `read_file` 运行期间第二条消息立即本地回显，footer 显示 `queued Ns │ queued`，第一条完成后第二条自动继续并输出 `queued ok`。
- [x] P1：2026-09-15 3 号窗口真实验证 model picker Enter 路径：`/model` 打开 picker 后选择当前 `traexCLIproxy/deepseek-v4-flash`，历史写入 `/model traexCLIproxy/deepseek-v4-flash` 和 `Switched model` recap，没有误进入普通 LLM thinking。
- [x] P1：2026-09-15 3 号窗口复测 reattach 时发现 `walle ps` 已 idle 但 TUI footer 仍显示 `running Ns`。根因是 attach replay 最后一条历史 state 可能是旧 busy 状态；已在 client replay 末尾追加 snapshot state，并复测 attach 后 footer 不再显示 running。
- [x] P1：2026-09-15 3 号窗口真实验证长历史滚动：`PageUp` 后 footer 固定且显示 `scroll 54%`，`End` 返回底部，输入框/footer 未错位。
- [x] P1：2026-09-15 3 号窗口真实验证 slash 只读路径：`/skill list`、`/skill get tmux-skill`、`/skill reload`、`/session`、`/mcp list` 都以 system recap 展示，长 skill 输出底栏稳定；未测试 `/mcp add/remove/enable/disable`，因为这些会改用户级 MCP 配置。
- [x] P1：2026-09-15 3 号窗口真实验证 `/provider` picker：打开后列出 provider，选择当前 `traexCLIproxy` 后进入 model picker；`ctrl+c` 可关闭 picker，普通输入草稿 `ctrl+c` 可清空。
- [x] P1：2026-09-15 80x24 窄屏真实 capture：错误信息换行可读，输入框/footer 不横向溢出，底栏显示 `scroll 0%`。
- [x] P1：2026-09-15 3 号窗口真实验证工具失败展示：读取不存在的 `/tmp/walle-this-file-should-not-exist-404.txt` 时显示 `✘ read_file(...)` 和截断后的 no such file 错误，随后模型能继续输出 `ok`。
- [x] P1：2026-09-15 隔离 runtime 验证 `/compress`：新会话切到 `deepseek-v4-flash` 后执行 `/compress`，返回 `Context not compressed (0 messages)`，未污染 3 号主矩阵会话。
- [x] P1：2026-09-15 `/mcp add/remove/enable/disable` 会写用户级 MCP 配置，未在真实 `~/.walle/mcp.json` 上执行；已新增隔离 HOME 的命令层测试覆盖 add/list/disable/enable/remove/list 全链路。
- [x] P1：2026-09-15 TUI slash hint 原先暗示 `/session <new|list|id>`，但 daemon 当前真实实现只支持 `/session` 当前 id；已把 hint 改为 `/session` / `current session`，避免 UI 复制不存在功能。
- [ ] P2：provider 登录类路径（例如 openai 登录）未做真实触发，避免启动外部认证流程；目前只验证了 `/provider` picker 和非登录 provider/model 路径。后续只有在用户明确授权“触发外部登录/provider 切换”后再测。
- [x] P2：2026-09-15 用户授权 provider/model 抽样后，在隔离 runtime 验证：`traexCLIproxy/deepseek-v4-flash` 简单回复成功；`ollama/gemma4:latest` 简单回复成功；`deepseek/deepseek-flash` 切换成功但请求失败，错误为 provider 不接受带点号工具名 `tools[0].function.name`，属于 provider/tool schema 兼容问题而非 TUI 卡死。
- [x] P2：2026-09-15 已在 `internal/llm` 增加 provider-safe 工具名别名层：发给 OpenAI-compatible/Claude provider 时把 `base.read_file`、`skill.skill`、`context.context`、`mcp.demo.lookup` 这类点号工具名改成下划线别名，模型返回后再恢复成本地原名；隔离 HOME 启动 `/tmp/walle-feedback-check --model deepseek/deepseek-flash` 真实 tmux 复测，`read_file`、`skill list`、`context inspect` 调用均成功。MCP 点号名已用 alias 单测覆盖，并新增 `internal/mcp` 真实 stdio 子进程测试覆盖 initialize、tools/list、tools/call。2026-09-15 并行验证发现 hash 后 alias 可能与第三个工具名碰撞，已修复 `uniqueToolAlias` 二次冲突登记/再散列逻辑并补充单测。
- [x] P2：2026-09-15 用户授权完整压力测后，在隔离 runtime 验证多工具连续调用：`grep`、`glob`、`read_file`、`write_file`、`edit` 连续展示并以 `stress ok` 收尾；模型未按本次压力提示调用 `list_dir`，但 `list_dir` 已在 base tool 矩阵单独通过。2026-09-15 追加隔离压力单测覆盖 40 列窄屏长命令/长错误工具行不溢出，以及 busy queue 后 `ctrl+c` 清空草稿但不丢 pending queue、不停止当前 run 的组合路径。
- [x] P2：2026-09-15 隔离 runtime 验证 `/stop` 异常组合：长 `exec_shell sleep` 运行中发送 `/stop`，TUI 显示 `stopped current run`，无空 assistant `⏺` 残留。
- [x] P2：2026-09-15 隔离 runtime 验证运行中 detach/reattach：长 `exec_shell sleep` 运行时重新 attach 可见 `running...` 工具行和 footer `running 1s`；等待完成后显示 `exit code: 0`、`done` 和 `reattach ok`，footer 回 idle。
- [x] P2：最终回复可能超过用户要求的“只回复路径和验证结果”。已在 Agent 本轮输入包含“只回复/只输出/仅回复/respond with/output only”等严格格式要求时追加临时 system reminder，要求最终回答只能包含用户指定字段；该约束只影响当前 run，不改写会话初始 system prompt，并用单测覆盖。
- [x] P2：新启动 `walle --session ...` 曾多次被系统直接 `killed`。已在交互 daemon 启动失败路径补充最小诊断：错误会包含 workspace、session id、model、debug 标记、daemon log 路径、退出状态和指定 session 文件大小提示，帮助区分本地模型内存压力、会话加载过大和启动期资源峰值；用隔离 HOME 单测覆盖。
