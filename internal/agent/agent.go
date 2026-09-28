// Package agent 实现 ReAct Agent 主循环，协调模型调用、工具执行、消息上下文和 Skill 注入。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentctx "github.com/Ozqi/walle/internal/context"
	"github.com/Ozqi/walle/internal/logger"
	"github.com/Ozqi/walle/internal/skill"
	"github.com/Ozqi/walle/internal/toolevent"
	"github.com/Ozqi/walle/internal/utils"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// =============================================================================
// Agent 配置和核心状态
// =============================================================================

// Agent 是有状态的 ReAct 执行器，协调 LLM、工具调用、消息上下文和 Skill 注入。
// 同一实例不应并发运行；Runtime 可在运行前替换模型、工具、Context Manager 和事件接收器。
type Agent struct {
	// 核心组件
	model   model.ToolCallingChatModel // LLM 模型
	toolMap map[string]tool.BaseTool   // 工具名称映射表

	// 配置
	config *Config // Agent 配置
	// 状态
	stateMu     sync.RWMutex // 保护当前轮次，供 TUI/daemon 跨 goroutine 读取
	currentTurn int
	// 上下文管理器
	ctxManager *agentctx.Manager // 消息历史管理
	// 技能管理器
	skillManager *skill.Manager // 技能注入管理
	// token 预算
	tokenBudget *utils.TokenBudget
	// 回调处理器
	callbacks *AgentCallbacks
	// 工具事件 sink
	toolEventSink func(toolevent.ToolEvent)
	// debug 日志只保留模型引用；当前 API key 仅用于从消息内容中固定掩码。
	modelRef    string
	debugSecret string
}

// Config 描述 Agent 的会话级配置，以及每轮模型调用共用的流式和压缩策略。
type Config struct {
	Name                string // Agent 名称
	MaxTotalTokens      int    // 整场会话累计 token 上限
	RepeatToolLimit     int    // 相同工具调用重复上限
	Debug               bool   // 是否启用调试
	ContextAutoCompress bool   // 是否自动触发上下文压缩
	DisableStream       bool   // 是否禁用流式模型调用；部分兼容供应商需要关闭
	SystemPrompt        string // 系统提示词
	PromptDir           string // prompt 文件目录，用于上下文压缩等内部 prompt
	ProjectDataDir      string // 项目 .walle 数据目录；为空时使用当前工作目录
}

// NewAgent 创建新的 Agent
// 参数:
//   - model: LLM 模型
//   - tools: 工具列表
//   - config: Agent 配置（包含系统提示词）
//
// 返回: Agent 实例和可能的错误
func NewAgent(model model.ToolCallingChatModel, tools []tool.BaseTool, config *Config) (*Agent, error) {
	// 1. 补齐运行默认值，并定位用户级和项目级 Skill 目录。
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if config.MaxTotalTokens == 0 {
		config.MaxTotalTokens = 1000000
	}
	if config.RepeatToolLimit == 0 {
		config.RepeatToolLimit = 5
	}

	configDir, err := utils.GetConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get config directory: %w", err)
	}
	projectDataDir := config.ProjectDataDir
	if projectDataDir == "" {
		var err error
		projectDataDir, err = utils.GetProjectDataDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get project data directory: %w", err)
		}
	}
	skillMgr := skill.NewManagerFromDirs(
		skill.Source{Scope: "global", Dir: filepath.Join(configDir, "skills")},
		skill.Source{Scope: "project", Dir: filepath.Join(projectDataDir, "skills")},
	)
	if err := skillMgr.LoadSkills(); err != nil {
		logger.DebugTag("SKILL", "Failed to load skills: %v", err)
	}

	// 2. 构建执行期工具索引；元数据读取失败的工具不会进入可调用映射。
	toolMap := make(map[string]tool.BaseTool)
	for _, t := range tools {
		info, err := t.Info(context.Background())
		if err != nil {
			continue
		}
		toolMap[info.Name] = t
	}

	tokenBudget := utils.NewTokenBudget(config.MaxTotalTokens)
	return &Agent{
		model:        model,
		toolMap:      toolMap,
		config:       config,
		ctxManager:   agentctx.NewManager(),
		skillManager: skillMgr,
		tokenBudget:  tokenBudget,
		callbacks:    NewAgentCallbacks(config.Debug, tokenBudget),
	}, nil
}

// =============================================================================
// Runtime 注入点：context manager、模型、工具和事件 sink
// =============================================================================

// SetCtxManager 设置上下文管理器（用于 session 持久化）
func (a *Agent) SetCtxManager(manager *agentctx.Manager) {
	a.ctxManager = manager
}

// SetDebugModel 更新 debug 请求日志使用的模型引用和敏感值掩码。
func (a *Agent) SetDebugModel(modelRef string, secret string) {
	a.modelRef = modelRef
	a.debugSecret = secret
}

// SetToolEventSink 设置当前 Agent 的工具事件接收器，并返回旧接收器。
// 参数：sink 接收 tool call/result/error/status 事件；nil 表示回退到 logger 默认输出。
// 调用层级：TUI/runtime -> SetToolEventSink -> exeToolCall/addToolResult。
// 步骤：只替换当前 Agent 实例字段，不改包级 logger sink。
func (a *Agent) SetToolEventSink(sink func(toolevent.ToolEvent)) func(toolevent.ToolEvent) {
	prev := a.toolEventSink
	a.toolEventSink = sink
	return prev
}

// TokenCallback 流式输出的回调函数类型
type TokenCallback func(token string)

// =============================================================================
// ReAct 辅助状态：重复工具防护和响应元数据合并
// =============================================================================

// toolRepeatGuard 工具重复调用防护结构
// 限制同一工具（含相同参数）被重复调用的次数，防止死循环
type toolRepeatGuard struct {
	limit    int
	attempts map[string]int
}

// newToolRepeatGuard 创建工具重复调用防护实例
// 参数:
//   - limit: 单工具（含相同参数）最大重复次数
//
// 返回: toolRepeatGuard 实例
func newToolRepeatGuard(limit int) *toolRepeatGuard {
	return &toolRepeatGuard{
		limit:    limit,
		attempts: make(map[string]int),
	}
}

// Check 检查工具调用是否超限
// 参数:
//   - toolCalls: 待检查的工具调用列表
//
// 返回: 超限返回错误，否则返回 nil
func (g *toolRepeatGuard) Check(toolCalls []schema.ToolCall) error {
	if g == nil || g.limit <= 0 {
		return nil
	}
	for _, tc := range toolCalls {
		args := tc.Function.Arguments
		trimmed := strings.TrimSpace(args)
		key := tc.Function.Name + ":"
		if trimmed != "" {
			var decoded interface{}
			if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
				key += trimmed
			} else if normalized, err := json.Marshal(decoded); err == nil {
				key += string(normalized)
			} else {
				key += trimmed
			}
		}
		g.attempts[key]++
		if g.attempts[key] > g.limit {
			return fmt.Errorf("repeated tool call detected after %d attempts: %s", g.limit, tc.Function.Name)
		}
	}
	return nil
}

// mergeStreamingResponseMeta 合并 LLM 流式响应元数据
// 参数:
//   - current: 当前累计的响应元数据（可能被 nil）
//   - incoming: 新到来的响应元数据
//
// 返回: 合并后的元数据（优先保留较大的 token 计数）
func mergeStreamingResponseMeta(current *schema.ResponseMeta, incoming *schema.ResponseMeta) *schema.ResponseMeta {
	if incoming == nil {
		return current
	}
	if current == nil {
		cloned := *incoming
		return &cloned
	}
	if incoming.FinishReason != "" {
		current.FinishReason = incoming.FinishReason
	}
	if incoming.Usage == nil {
		return current
	}
	if current.Usage == nil {
		cloned := *incoming.Usage
		current.Usage = &cloned
		return current
	}
	if incoming.Usage.PromptTokens > current.Usage.PromptTokens {
		current.Usage.PromptTokens = incoming.Usage.PromptTokens
		current.Usage.PromptTokenDetails = incoming.Usage.PromptTokenDetails
	}
	if incoming.Usage.CompletionTokens > current.Usage.CompletionTokens {
		current.Usage.CompletionTokens = incoming.Usage.CompletionTokens
	}
	if incoming.Usage.TotalTokens > current.Usage.TotalTokens {
		current.Usage.TotalTokens = incoming.Usage.TotalTokens
	}
	if current.Usage.TotalTokens == 0 {
		current.Usage.TotalTokens = current.Usage.PromptTokens + current.Usage.CompletionTokens
	}
	return current
}

// RunStream 运行 Agent 并流式输出响应
// 参数:
//   - ctx: Go 标准上下文
//   - messageCtx: 消息上下文
//   - input: 用户输入
//   - onToken: 正文 token 回调函数（每个 token 会调用一次）
//   - onReasoning: 可选 thinking/reasoning token 回调函数
//
// 返回: 完整响应内容和可能的错误
func (a *Agent) RunStream(ctx context.Context, messageCtx *agentctx.Context, input string, onToken TokenCallback, onReasoning ...TokenCallback) (string, error) {
	return a.RunStreamWithOptions(ctx, messageCtx, input, onToken, nil, onReasoning...)
}

// =============================================================================
// ReAct 主循环：注入上下文 -> 调模型 -> 执行工具 -> 回写消息
// =============================================================================

// RunStreamWithOptions 运行 Agent，并为本次模型调用追加临时 model options。
// 参数：opts 只影响当前 RunStream 调用，不改变 Agent 持有的模型和工具列表。
// 调用层级：Runtime 交互执行 -> RunStreamWithOptions -> model.Stream。
// 执行边界：模型流读取可与工具执行重叠，但工具由单 worker 串行执行；assistant tool-call
// 消息先写入上下文，再按派发顺序写入 tool result。空响应最多追加两次 user reminder 后重试。
func (a *Agent) RunStreamWithOptions(ctx context.Context, messageCtx *agentctx.Context, input string, onToken TokenCallback, opts []model.Option, onReasoning ...TokenCallback) (string, error) {
	var reasoningCallback TokenCallback
	if len(onReasoning) > 0 {
		reasoningCallback = onReasoning[0]
	}

	// 1. 初始化会话并追加本轮用户消息。
	if err := a.ensureConversationSetup(messageCtx); err != nil {
		return "", err
	}

	ctx = agentctx.WithToolRuntime(ctx, a.ctxManager, messageCtx)
	userMsg := &schema.Message{
		Role:    schema.User,
		Content: input,
	}
	if err := a.ctxManager.AddMessage(messageCtx, userMsg); err != nil {
		return "", fmt.Errorf("failed to add user message: %w", err)
	}
	if strictFinalOutputRequested(input) {
		msg := &schema.Message{Role: schema.System, Content: strictFinalOutputReminder}
		if err := a.ctxManager.AddMessage(messageCtx, msg); err != nil {
			return "", fmt.Errorf("failed to add final output reminder: %w", err)
		}
	}

	// 2. 在进入 ReAct 循环前按配置压缩过长上下文。
	if a.config.ContextAutoCompress && a.ctxManager.ShouldCompress(messageCtx) {
		before, after, err := a.ctxManager.LMCompress(ctx, messageCtx, a.model, a.config.PromptDir)
		if err != nil {
			return "", fmt.Errorf("failed to compress context: %w", err)
		}
		logger.DebugTag("CTX", "Context compressed: %d -> %d messages", before, after)
	}

	// 3. 逐轮调用模型；有工具调用时执行并回写，无工具调用时提交最终回答。
	repeatGuard := newToolRepeatGuard(a.config.RepeatToolLimit)
	failedTools := make(map[string]bool) // 本次运行内曾失败的工具名；成功重试时写入恢复提示，避免最终回答沿用旧失败结论。
	emptyResponseRetries := 0

	for turn := 0; ; turn++ {
		a.setCurrentTurn(turn + 1)
		if a.config.Debug {
			logger.DebugTag("REACT", "Turn %d", turn+1)
		}

		// 读取当前完整消息快照，作为本轮模型输入。
		messages, err := a.ctxManager.GetMessages(messageCtx)
		if err != nil {
			return "", fmt.Errorf("failed to get messages: %w", err)
		}
		if a.config.Debug {
			logger.DebugTag("CTX", "Messages=%d", len(messages))
			a.logLLMRequest(messages, len(opts))
		}

		// 调用 LLM，并通过 Callback 记录本轮模型状态和 token 使用量。
		cb := a.callbacks
		cb.OnModelStart(ctx, nil, &model.CallbackInput{Messages: messages})

		// 非流式路径：兼容不稳定的 OpenAI-compatible 供应商，语义仍和流式路径一致。
		if a.config.DisableStream {
			msg, err := a.model.Generate(ctx, messages, opts...)
			if err != nil {
				cb.OnModelError(ctx, nil, err)
				return "", fmt.Errorf("LLM generate failed: %w", err)
			}
			if msg == nil {
				err := fmt.Errorf("LLM generate returned nil message")
				cb.OnModelError(ctx, nil, err)
				return "", err
			}

			var tokenUsage *model.TokenUsage
			if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
				tokenUsage = &model.TokenUsage{
					PromptTokens:     msg.ResponseMeta.Usage.PromptTokens,
					CompletionTokens: msg.ResponseMeta.Usage.CompletionTokens,
					TotalTokens:      msg.ResponseMeta.Usage.TotalTokens,
				}
			}
			cb.OnModelEnd(ctx, nil, &model.CallbackOutput{Message: msg, TokenUsage: tokenUsage})

			if msg.ReasoningContent != "" && reasoningCallback != nil {
				reasoningCallback(msg.ReasoningContent)
			}
			if msg.Content != "" && onToken != nil {
				onToken(msg.Content)
			}

			toolCalls := make([]schema.ToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				if tc.Function.Name == "" {
					logger.WarnTag("TOOL", "Skipping incomplete tool call: id=%s name=%s", tc.ID, tc.Function.Name)
					continue
				}
				toolCalls = append(toolCalls, tc)
			}

			finalMessage := &schema.Message{
				Role:             schema.Assistant,
				Content:          msg.Content,
				ReasoningContent: msg.ReasoningContent,
				ToolCalls:        toolCalls,
				ResponseMeta:     msg.ResponseMeta,
				Extra:            msg.Extra,
			}

			if len(finalMessage.ToolCalls) > 0 {
				cb.LogToolCalls(finalMessage.ToolCalls)
				if err := a.ctxManager.AddMessage(messageCtx, finalMessage); err != nil {
					return "", fmt.Errorf("failed to add assistant message: %w", err)
				}
				for idx, tc := range finalMessage.ToolCalls {
					res := a.executeToolWithRepeatGuard(ctx, repeatGuard, toolRequest{idx: idx, tc: tc})
					recovered := res.err == nil && failedTools[tc.Function.Name]
					if err := a.addToolResult(messageCtx, tc, res.result, res.err, recovered); err != nil {
						return "", fmt.Errorf("tool execution failed: %w", err)
					}
					if res.err != nil {
						failedTools[tc.Function.Name] = true
					}
				}
				continue
			}

			if msg.Content != "" {
				if err := a.ctxManager.AddMessage(messageCtx, finalMessage); err != nil {
					return "", fmt.Errorf("failed to add assistant message: %w", err)
				}
			} else {
				logger.DebugTag("REACT", "Skipping empty assistant message")
				if emptyResponseRetries >= 2 {
					return "", fmt.Errorf("LLM returned empty response without tool calls")
				}
				emptyResponseRetries++
				if err := a.addEmptyResponseReminder(messageCtx); err != nil {
					return "", err
				}
				continue
			}
			return msg.Content, nil
		}

		// 流式路径：边读 chunk 边收集可执行 ToolCall，工具执行和模型读取可重叠。
		streamCtx, streamCancel := context.WithCancel(ctx)
		reader, err := a.model.Stream(streamCtx, messages, opts...)
		if err != nil {
			streamCancel()
			cb.OnModelError(ctx, nil, err)
			return "", fmt.Errorf("LLM stream failed: %w", err)
		}

		var fullContent strings.Builder
		var fullReasoning strings.Builder
		var fullExtra map[string]any
		chunkCount := 0
		collector := newToolCollector()
		var responseMeta *schema.ResponseMeta

		toolQueue := make(chan toolRequest, 8)
		toolResultCh := make(chan execResult, 8)
		queuedCalls := make([]schema.ToolCall, 0)
		toolCtx, toolCancel := context.WithCancel(ctx)
		a.runToolWorker(toolCtx, repeatGuard, toolQueue, toolResultCh)
		closeToolQueue := func() {
			close(toolQueue)
			toolCancel()
		}

		// 读取 goroutine 隔离可能阻塞的 Recv，使主循环可以实施空闲超时。
		for {
			type recvResult struct {
				chunk *schema.Message
				err   error
			}
			recvCh := make(chan recvResult, 1)
			go func() {
				chunk, err := reader.Recv()
				recvCh <- recvResult{chunk: chunk, err: err}
			}()

			var chunk *schema.Message
			var err error
			select {
			case res := <-recvCh:
				chunk, err = res.chunk, res.err
			case <-time.After(90 * time.Second):
				streamCancel()
				reader.Close()
				toolCancel()
				closeToolQueue()
				cb.OnModelError(ctx, nil, context.DeadlineExceeded)
				return "", fmt.Errorf("LLM stream idle timeout: no text or tool call chunk received for 90s")
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				reader.Close()
				streamCancel()
				toolCancel()
				closeToolQueue()
				cb.OnModelError(ctx, nil, err)
				return "", fmt.Errorf("stream read failed: %w", err)
			}

			chunkCount++
			responseMeta = mergeStreamingResponseMeta(responseMeta, chunk.ResponseMeta)
			fullExtra = mergeStreamingMessageExtra(fullExtra, chunk.Extra)

			if len(chunk.ToolCalls) > 0 {
				cb.LogChunk(chunk, chunkCount)

				for _, tc := range collector.Add(chunk.ToolCalls) {
					idx := len(queuedCalls)
					queuedCalls = append(queuedCalls, tc)
					toolQueue <- toolRequest{idx: idx, tc: tc}
				}
			}

			if chunk.ReasoningContent != "" {
				fullReasoning.WriteString(chunk.ReasoningContent)
				if reasoningCallback != nil {
					reasoningCallback(chunk.ReasoningContent)
				}
			}

			if chunk.Content != "" {
				fullContent.WriteString(chunk.Content)
				if onToken != nil {
					onToken(chunk.Content)
				}
			}
		}
		reader.Close()
		streamCancel()

		// EOF 后把无参数调用归一化并派发，再关闭队列等待单 worker 收尾。
		for _, tc := range collector.PendingRunnableCalls() {
			idx := len(queuedCalls)
			queuedCalls = append(queuedCalls, tc)
			toolQueue <- toolRequest{idx: idx, tc: tc}
		}
		close(toolQueue)

		content := fullContent.String()
		reasoningContent := fullReasoning.String()

		// 转换 token usage 类型
		var tokenUsage *model.TokenUsage
		if responseMeta != nil && responseMeta.Usage != nil {
			tokenUsage = &model.TokenUsage{
				PromptTokens:     responseMeta.Usage.PromptTokens,
				CompletionTokens: responseMeta.Usage.CompletionTokens,
				TotalTokens:      responseMeta.Usage.TotalTokens,
			}
		}
		cb.OnModelEnd(ctx, nil, &model.CallbackOutput{
			Message:    &schema.Message{Content: content, ResponseMeta: responseMeta},
			TokenUsage: tokenUsage,
		})

		toolCalls := make([]schema.ToolCall, len(queuedCalls))
		copy(toolCalls, queuedCalls)

		// 过滤掉没有工具名的调用（流式输出中不完整的调用），避免发给 LLM 造成格式错误
		var validCalls []schema.ToolCall
		for _, tc := range toolCalls {
			if tc.Function.Name == "" {
				logger.WarnTag("TOOL", "Skipping incomplete tool call: id=%s name=%s", tc.ID, tc.Function.Name)
				continue
			}
			validCalls = append(validCalls, tc)
		}
		toolCalls = validCalls

		// 收集已执行的结果并按 queuedCalls 下标还原顺序；此处会等待 worker 关闭结果通道。
		toolResults := make([]execResult, len(queuedCalls))
		for res := range toolResultCh {
			toolResults[res.idx] = res
		}
		toolCancel()

		finalMessage := &schema.Message{
			Role:             schema.Assistant,
			Content:          content,
			ReasoningContent: reasoningContent,
			ToolCalls:        toolCalls,
			ResponseMeta:     responseMeta,
			Extra:            fullExtra,
		}

		// 有工具调用时，先提交 assistant 消息，再按调用顺序提交工具结果。
		if len(finalMessage.ToolCalls) > 0 {
			emptyResponseRetries = 0
			cb.LogToolCalls(finalMessage.ToolCalls)

			if err := a.ctxManager.AddMessage(messageCtx, finalMessage); err != nil {
				return "", fmt.Errorf("failed to add assistant message: %w", err)
			}

			for _, res := range toolResults {
				// 只添加有效工具的结果（id 不为空）
				if res.tc.ID == "" {
					continue
				}
				recovered := res.err == nil && failedTools[res.tc.Function.Name]
				if err := a.addToolResult(messageCtx, res.tc, res.result, res.err, recovered); err != nil {
					return "", fmt.Errorf("tool execution failed: %w", err)
				}
				if res.err != nil {
					failedTools[res.tc.Function.Name] = true
				}
			}

			continue
		}

		// 没有工具调用时提交最终回答；空响应通过 user reminder 驱动下一轮继续。
		if content != "" {
			if err := a.ctxManager.AddMessage(messageCtx, finalMessage); err != nil {
				return "", fmt.Errorf("failed to add assistant message: %w", err)
			}
		} else {
			logger.DebugTag("REACT", "Skipping empty assistant message")
			if emptyResponseRetries >= 2 {
				return "", fmt.Errorf("LLM returned empty response without tool calls")
			}
			emptyResponseRetries++
			if err := a.addEmptyResponseReminder(messageCtx); err != nil {
				return "", err
			}
			continue
		}

		return content, nil
	}
}

func (a *Agent) addEmptyResponseReminder(messageCtx *agentctx.Context) error {
	msg := &schema.Message{
		Role:    schema.User,
		Content: "上一轮没有输出也没有工具调用。任务尚未完成，下一轮必须立即调用一个必要工具继续推进；如果已经具备足够信息且任务要求写文件，必须先调用写文件工具，再读取目标文件验证。不要只思考、不要只总结、不要请求确认；只有确实无法继续时才输出明确阻塞原因。",
	}
	if err := a.ctxManager.AddMessage(messageCtx, msg); err != nil {
		return fmt.Errorf("failed to add empty response reminder: %w", err)
	}
	return nil
}

const strictFinalOutputReminder = "本轮用户明确要求了最终输出格式。最终回答只能包含用户指定的字段或内容；不要添加寒暄、标题、解释、过程总结、额外验证细节或下一步建议。"

func strictFinalOutputRequested(input string) bool {
	input = strings.TrimSpace(input)
	if input == "" {
		return false
	}
	markers := []string{
		"只回复",
		"只输出",
		"最后只回复",
		"最终只回复",
		"仅回复",
		"仅输出",
		"不要添加",
		"do not add",
		"respond with",
		"output only",
		"only output",
		"reply only",
	}
	lower := strings.ToLower(input)
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// =============================================================================
// 只读访问器和运行时替换点
// =============================================================================

// GetSkillManager 获取技能管理器
func (a *Agent) GetSkillManager() *skill.Manager {
	return a.skillManager
}

// SetModel 设置模型
func (a *Agent) SetModel(model model.ToolCallingChatModel) {
	a.model = model
}

// SetSystemPrompt 更新后续新会话注入的 system prompt。
// 已存在消息的当前会话不会被重写，避免破坏历史上下文。
func (a *Agent) SetSystemPrompt(prompt string) {
	if a == nil || a.config == nil {
		return
	}
	a.config.SystemPrompt = prompt
}

// GetModel 返回当前绑定的 LLM 模型
// 返回: ToolCallingChatModel 实例，可能为 nil
func (a *Agent) GetModel() model.ToolCallingChatModel {
	if a == nil {
		return nil
	}
	return a.model
}

// SetTools 设置工具列表
func (a *Agent) SetTools(tools []tool.BaseTool) {
	a.toolMap = make(map[string]tool.BaseTool)
	for _, t := range tools {
		info, err := t.Info(context.Background())
		if err != nil {
			continue
		}
		a.toolMap[info.Name] = t
	}
}

// Name 返回 Agent 名称
// 返回: Agent 配置中的名称字符串
func (a *Agent) Name() string {
	if a == nil || a.config == nil {
		return ""
	}
	return a.config.Name
}

// TokenUsage 返回最近一次请求的输入 token、会话累计 token 和模型上下文窗口。
func (a *Agent) TokenUsage() (prompt int, total int, window int) {
	if a == nil || a.tokenBudget == nil {
		return 0, 0, 0
	}
	return a.tokenBudget.Usage()
}

// SetContextWindow 设置当前模型实际使用的上下文窗口。
func (a *Agent) SetContextWindow(window int) {
	if a == nil || a.tokenBudget == nil {
		return
	}
	a.tokenBudget.SetContextWindow(window)
}

// CurrentTurn 返回当前 ReAct 轮次。
// 空闲时表示最近一次运行停留的轮次；TUI 只把它作为运行时元信息展示。
func (a *Agent) CurrentTurn() int {
	if a == nil {
		return 0
	}
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.currentTurn
}

func (a *Agent) setCurrentTurn(turn int) {
	a.stateMu.Lock()
	a.currentTurn = turn
	a.stateMu.Unlock()
}

// ensureConversationSetup 只为全新会话注入 system prompt 和已加载的 Skill。
// 从 Session 恢复且已有消息的会话不会重复注入。
func (a *Agent) ensureConversationSetup(messageCtx *agentctx.Context) error {
	messages, _ := a.ctxManager.GetMessages(messageCtx)
	if len(messages) > 0 {
		return nil
	}

	if a.config.SystemPrompt != "" {
		systemMsg := &schema.Message{
			Role:    schema.System,
			Content: a.config.SystemPrompt,
		}
		if err := a.ctxManager.AddMessage(messageCtx, systemMsg); err != nil {
			return fmt.Errorf("failed to add system prompt: %w", err)
		}
	}

	if err := a.injectSkills(messageCtx); err != nil {
		return fmt.Errorf("failed to inject skills: %w", err)
	}

	return nil
}

// injectSkills 将已加载的技能作为独立消息注入到上下文。
func (a *Agent) injectSkills(messageCtx *agentctx.Context) error {
	skills := a.skillManager.ListSkills()
	for _, skill := range skills {
		skillMsg := &schema.Message{
			Role:    schema.System,
			Content: fmt.Sprintf("# Skill: %s\n\n%s", skill.Name, skill.Content),
		}
		if err := a.ctxManager.AddMessage(messageCtx, skillMsg); err != nil {
			return fmt.Errorf("failed to add skill %s: %w", skill.Name, err)
		}
	}
	return nil
}
