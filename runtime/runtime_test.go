// 功能：验证 public Runtime SDK 的 DTO 转换、事件转换和关闭状态保护。
// 调用方：go test ./runtime；不创建真实 internal runtime，不访问 provider、daemon 或用户配置。
// 全局状态：无。
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	intruntime "github.com/Ozqi/walle/internal/runtime"
	"github.com/Ozqi/walle/internal/toolevent"
)

func TestPublicInfoAndEventConversion(t *testing.T) {
	info := fromInternalInfo(intruntime.Info{
		SessionID:  "session-1",
		PromptBase: "main",
		ModelRef:   "provider/model",
		ProjectDir: "/tmp/work",
		Turn:       7,
	})
	if info.SessionID != "session-1" || info.ModelRef != "provider/model" || info.Turn != 7 {
		t.Fatalf("fromInternalInfo = %#v, want public info fields", info)
	}

	event := fromToolEvent(toolevent.ToolEvent{Kind: "call", Name: "base.read_file", Args: `{"path":"README.md"}`, Concurrent: true}, 3)
	if event.Type != EventTool || event.Name != "base.read_file" || event.Turn != 3 || !event.Concurrent {
		t.Fatalf("fromToolEvent = %#v, want public tool event", event)
	}
}

func TestPublicRuntimeRunUsesOpenAICompatibleFlow(t *testing.T) {
	isolatePublicRuntimeEnv(t)
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".walle")
	if err := os.MkdirAll(filepath.Join(configDir, "prompt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "prompt", "main.md"), []byte("You are a test agent."), 0o644); err != nil {
		t.Fatal(err)
	}

	var sawTools bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("request path = %s, want /v1/chat/completions", r.URL.Path)
		}
		var body struct {
			Model string `json:"model"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "flow-model" {
			t.Fatalf("model = %q, want flow-model", body.Model)
		}
		for _, item := range body.Tools {
			if item.Function.Name == "base_read_file" || item.Function.Name == "context_context" {
				sawTools = true
			}
			if strings.Contains(item.Function.Name, ".") {
				t.Fatalf("remote tool name %q still contains dot", item.Function.Name)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"sdk ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	t.Setenv("LLM_LOCAL_FORMAT", "openai")
	t.Setenv("LLM_LOCAL_BASE_URL", server.URL+"/v1")
	t.Setenv("LLM_LOCAL_API_KEY", "dummy")
	t.Setenv("LLM_LOCAL_STREAM", "false")
	rt, err := New(context.Background(), Options{ProjectDir: workspace, ModelRef: "local/flow-model", LLMFormat: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	var events []Event
	result, err := rt.Run(context.Background(), "hello", WithEventHandler(func(event Event) {
		events = append(events, event)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Response != "sdk ok" || result.ModelRef != "local/flow-model" || result.SessionID == "" {
		t.Fatalf("Run result = %#v, want response/model/session", result)
	}
	if !sawTools {
		t.Fatal("OpenAI-compatible request did not include bound tool schema aliases")
	}
	if len(events) < 4 || events[0].Type != EventUser || events[1].Type != EventState || events[len(events)-2].Type != EventDone || events[len(events)-1].Type != EventState {
		t.Fatalf("events = %#v, want user/state/.../done/state", events)
	}
}

func isolatePublicRuntimeEnv(t *testing.T) {
	t.Helper()
	old := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(key, "LLM_") || key == "DEEPSEEK_API_KEY" {
			old[key] = value
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		for key := range old {
			_ = os.Unsetenv(key)
		}
		for key, value := range old {
			_ = os.Setenv(key, value)
		}
	})
}

func TestPublicRuntimeClosedAndBusyGuards(t *testing.T) {
	if err := (*Runtime)(nil).Close(); err != nil {
		t.Fatalf("nil Close error = %v, want nil", err)
	}
	if _, err := (*Runtime)(nil).Run(context.Background(), "hello"); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil Run error = %v, want ErrClosed", err)
	}

	r := &Runtime{busy: true}
	if err := r.Close(); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy Close error = %v, want ErrBusy", err)
	}

	r.busy = false
	if err := r.Close(); err != nil {
		t.Fatalf("Close empty runtime error = %v, want nil", err)
	}
	if _, err := r.Run(context.Background(), "hello"); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed Run error = %v, want ErrClosed", err)
	}
}
