// 功能：为 OpenAI-compatible/Claude 模型适配不接受点号的工具名，并在本地恢复原始工具名。
// 调用方：internal/llm/client.go 创建 openai/claude 模型后包裹；Runtime 绑定工具和 Agent 执行仍使用原名。
// 全局状态：无；每个 safeToolNameModel 实例持有一次 WithTools 生成的名称映射。
package llm

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const maxToolAliasLen = 64

// safeToolNameModel 把本地工具名映射成 OpenAI function name 兼容别名。
// 参数和返回：inner 是真实模型；WithTools 建立 local<->remote 映射后返回新包装器。
// 副作用：只改发给 provider 的 ToolInfo/ToolCall 名称，不修改 Agent 工具表和消息上下文原始名称。
type safeToolNameModel struct {
	inner      model.ToolCallingChatModel
	toRemote   map[string]string // 本地工具名 -> provider 可接受别名
	fromRemote map[string]string // provider 别名 -> 本地工具名
}

func withSafeToolNames(inner model.ToolCallingChatModel) model.ToolCallingChatModel {
	return &safeToolNameModel{inner: inner}
}

func (m *safeToolNameModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	aliased, toRemote, fromRemote, err := aliasToolInfos(infos)
	if err != nil {
		return nil, err
	}
	inner, err := m.inner.WithTools(aliased)
	if err != nil {
		return nil, err
	}
	return &safeToolNameModel{inner: inner, toRemote: toRemote, fromRemote: fromRemote}, nil
}

func (m *safeToolNameModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	msg, err := m.inner.Generate(ctx, m.toRemoteMessages(input), opts...)
	if err != nil {
		return nil, err
	}
	return m.toLocalMessage(msg), nil
}

func (m *safeToolNameModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	reader, err := m.inner.Stream(ctx, m.toRemoteMessages(input), opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderWithConvert(reader, func(msg *schema.Message) (*schema.Message, error) {
		return m.toLocalMessage(msg), nil
	}), nil
}

func (m *safeToolNameModel) toRemoteMessages(input []*schema.Message) []*schema.Message {
	return mapMessageToolNames(input, m.toRemote)
}

func (m *safeToolNameModel) toLocalMessage(msg *schema.Message) *schema.Message {
	if msg == nil || len(msg.ToolCalls) == 0 || len(m.fromRemote) == 0 {
		return msg
	}
	clone := *msg
	clone.ToolCalls = mapToolCalls(msg.ToolCalls, m.fromRemote)
	return &clone
}

func aliasToolInfos(infos []*schema.ToolInfo) ([]*schema.ToolInfo, map[string]string, map[string]string, error) {
	aliased := make([]*schema.ToolInfo, len(infos))
	toRemote := make(map[string]string, len(infos))
	fromRemote := make(map[string]string, len(infos))
	for i, info := range infos {
		if info == nil {
			return nil, nil, nil, fmt.Errorf("tool info cannot be nil")
		}
		alias := uniqueToolAlias(info.Name, fromRemote)
		clone := *info
		clone.Name = alias
		aliased[i] = &clone
		toRemote[info.Name] = alias
		fromRemote[alias] = info.Name
	}
	return aliased, toRemote, fromRemote, nil
}

func uniqueToolAlias(name string, used map[string]string) string {
	alias := safeToolAlias(name)
	if alias == "" {
		alias = "tool"
	}
	if previous, exists := used[alias]; !exists || previous == name {
		used[alias] = name
		return alias
	}
	base := alias
	for attempt := 0; ; attempt++ {
		suffixSource := name
		if attempt > 0 {
			suffixSource = fmt.Sprintf("%s#%d", name, attempt)
		}
		suffix := "_" + shortHash(suffixSource)
		baseLen := maxToolAliasLen - len(suffix)
		if baseLen < 1 {
			baseLen = 1
		}
		alias = base
		if len(alias) > baseLen {
			alias = alias[:baseLen]
		}
		alias += suffix
		if previous, exists := used[alias]; !exists || previous == name {
			used[alias] = name
			return alias
		}
	}
}

func safeToolAlias(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	alias := strings.Trim(b.String(), "_")
	if len(alias) > maxToolAliasLen {
		suffix := "_" + shortHash(name)
		alias = alias[:maxToolAliasLen-len(suffix)] + suffix
	}
	return alias
}

func shortHash(text string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(text))
	return fmt.Sprintf("%08x", h.Sum32())
}

func mapMessageToolNames(input []*schema.Message, names map[string]string) []*schema.Message {
	if len(names) == 0 {
		return input
	}
	out := make([]*schema.Message, len(input))
	for i, msg := range input {
		if msg == nil || len(msg.ToolCalls) == 0 {
			out[i] = msg
			continue
		}
		clone := *msg
		clone.ToolCalls = mapToolCalls(msg.ToolCalls, names)
		out[i] = &clone
	}
	return out
}

func mapToolCalls(calls []schema.ToolCall, names map[string]string) []schema.ToolCall {
	out := make([]schema.ToolCall, len(calls))
	copy(out, calls)
	for i := range out {
		if mapped := names[out[i].Function.Name]; mapped != "" {
			out[i].Function.Name = mapped
		}
	}
	return out
}
