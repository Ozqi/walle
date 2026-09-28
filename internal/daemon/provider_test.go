// 功能：验证 daemon provider/model picker 分支，包括 OpenAI 登录触发点的可替换测试路径。
// 调用方：go test ./internal/daemon；使用 stub login/model catalog，不触发真实 OAuth 或 provider 网络请求。
// 全局状态：测试临时替换包级 startOpenAILoginFunc/providerModelsFunc，并在 cleanup 恢复。
package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Ozqi/walle/internal/agent"
	agentrt "github.com/Ozqi/walle/internal/runtime"
)

func TestProviderOpenAIStartsLoginThenPublishesModels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldLogin := startOpenAILoginFunc
	oldModels := providerModelsFunc
	t.Cleanup(func() {
		startOpenAILoginFunc = oldLogin
		providerModelsFunc = oldModels
	})

	done := make(chan error, 1)
	loginCalled := false
	modelProvider := make(chan string, 1)
	startOpenAILoginFunc = func(ctx context.Context) (string, <-chan error, error) {
		loginCalled = true
		return "http://127.0.0.1/login", done, nil
	}
	providerModelsFunc = func(ctx context.Context, provider string) ([]string, error) {
		modelProvider <- provider
		return []string{"gpt-test"}, nil
	}

	session := NewDaemonSession(context.Background(), &agentrt.Runtime{Agent: &agent.Agent{}, ModelRef: "deepseek/current"}, "interactive-1", "workspace")
	if err := session.Submit("/provider openai"); err != nil {
		t.Fatal(err)
	}
	if !loginCalled {
		t.Fatal("/provider openai did not start login path")
	}
	if !eventuallyEvent(session, func(event ProcessEvent) bool {
		return event.Type == ProcessEventSystem && strings.Contains(event.Text, "Open this URL to sign in with ChatGPT")
	}) {
		t.Fatalf("events = %#v, want login URL system event", session.events)
	}

	done <- nil
	if !eventuallyEvent(session, func(event ProcessEvent) bool {
		return event.Type == ProcessEventPicker && event.Kind == "model" && event.Name == "openai" && len(event.Options) == 1 && event.Options[0] == "gpt-test"
	}) {
		t.Fatalf("events = %#v, want openai model picker after login", session.events)
	}
	if got := <-modelProvider; got != "openai" {
		t.Fatalf("providerModels called with %q, want openai", got)
	}
}

func TestProviderNonOpenAIUsesModelCatalogWithoutLogin(t *testing.T) {
	oldLogin := startOpenAILoginFunc
	oldModels := providerModelsFunc
	t.Cleanup(func() {
		startOpenAILoginFunc = oldLogin
		providerModelsFunc = oldModels
	})

	startOpenAILoginFunc = func(ctx context.Context) (string, <-chan error, error) {
		t.Fatal("non-openai provider should not start login")
		return "", nil, nil
	}
	providerModelsFunc = func(ctx context.Context, provider string) ([]string, error) {
		if provider != "deepseek" {
			t.Fatalf("providerModels called with %q, want deepseek", provider)
		}
		return []string{"deepseek-test"}, nil
	}

	session := NewDaemonSession(context.Background(), &agentrt.Runtime{Agent: &agent.Agent{}, ModelRef: "local/current"}, "interactive-1", "workspace")
	if err := session.Submit("/provider deepseek"); err != nil {
		t.Fatal(err)
	}
	if !eventuallyEvent(session, func(event ProcessEvent) bool {
		return event.Type == ProcessEventPicker && event.Kind == "model" && event.Name == "deepseek" && len(event.Options) == 1 && event.Options[0] == "deepseek-test"
	}) {
		t.Fatalf("events = %#v, want deepseek model picker", session.events)
	}
}

func eventuallyEvent(session *DaemonSession, match func(ProcessEvent) bool) bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		session.mu.Lock()
		events := append([]ProcessEvent(nil), session.events...)
		session.mu.Unlock()
		for _, event := range events {
			if match(event) {
				return true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
