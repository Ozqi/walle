// 功能：验证 Context Manager 的消息写入、持久化恢复、检查、编辑和压缩语义。
// 调用方：go test ./internal/context；使用临时 session 目录，不读取用户真实 ~/.walle/sessions。
// 全局状态：无；每个测试用例隔离 Store 目录和内存 Context。
package context

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestManagerPersistsAndRestoresSessionMessages(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	ctx, err := mgr.CreateContext("session-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.AddMessage(ctx, &schema.Message{Role: schema.User, Content: "hello session"}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.AddMessage(ctx, &schema.Message{Role: schema.Assistant, Content: "hello back"}); err != nil {
		t.Fatal(err)
	}

	restoredMgr := NewManager(dir)
	restored, err := restoredMgr.CreateContext("session-a")
	if err != nil {
		t.Fatal(err)
	}
	messages, err := restoredMgr.GetMessages(restored)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Content != "hello session" || messages[1].Content != "hello back" {
		t.Fatalf("restored messages = %#v, want persisted user/assistant", messages)
	}
	if got := restoredMgr.GetSessionTitle(restored); got != "hello session" {
		t.Fatalf("session title = %q, want first user message", got)
	}
}

func TestManagerInspectPinEditAndAudit(t *testing.T) {
	mgr := NewManager(t.TempDir())
	ctx, err := mgr.CreateContext("session-b")
	if err != nil {
		t.Fatal(err)
	}
	messages := []*schema.Message{
		{Role: schema.System, Content: "system prompt"},
		{Role: schema.User, Content: "draft one"},
		{Role: schema.Assistant, Content: "answer one"},
	}
	for _, msg := range messages {
		if err := mgr.AddMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	if err := mgr.PinRange(ctx, ContextRange{Start: 1, End: 1, Reason: "keep"}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.EditMessage(ctx, 2, "answer edited", "fix typo"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.EditMessage(ctx, 1, "blocked", "pinned"); err == nil {
		t.Fatal("EditMessage pinned message succeeded, want error")
	}

	inspect, err := mgr.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inspect.MessageCount != 3 || inspect.Messages[2].Preview != "answer edited" {
		t.Fatalf("inspect = %#v, want edited message", inspect)
	}
	if !hasFlag(inspect.Messages[0].Flags, "protected") || !hasFlag(inspect.Messages[1].Flags, "pinned") {
		t.Fatalf("inspect flags = %#v, want protected system and pinned user", inspect.Messages)
	}
	if events := mgr.Audit(ctx); len(events) != 2 || events[0].Op != "pin" || events[1].Op != "edit" {
		t.Fatalf("audit = %#v, want pin then edit", events)
	}
}

func TestCompressFallbackKeepsSystemAndRecentHistory(t *testing.T) {
	mgr := NewManager(t.TempDir())
	ctx, err := mgr.CreateContext("session-c")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.AddMessage(ctx, &schema.Message{Role: schema.System, Content: "system prompt"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxMessages+5; i++ {
		if err := mgr.AddMessage(ctx, &schema.Message{Role: schema.User, Content: "msg" + string(rune('A'+i%26))}); err != nil {
			t.Fatal(err)
		}
	}
	before, after, err := mgr.Compress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before != MaxMessages+6 || after != KeepRecentMessages+1 {
		t.Fatalf("Compress counts = %d -> %d, want %d -> %d", before, after, MaxMessages+6, KeepRecentMessages+1)
	}
	got, err := mgr.GetMessages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Role != schema.System || got[0].Content != "system prompt" {
		t.Fatalf("first message after compress = %#v, want system prompt", got[0])
	}
	if strings.HasPrefix(got[1].Content, "msgA") {
		t.Fatalf("compress kept oldest history at index 1: %#v", got[1])
	}
}

func hasFlag(flags []string, want string) bool {
	for _, flag := range flags {
		if flag == want {
			return true
		}
	}
	return false
}
