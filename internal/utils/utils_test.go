// 功能：验证配置加载、provider 发现和模型前缀 prompt 拼接规则。
// 调用方：go test ./internal/utils；使用临时 HOME 和 prompt 目录，不读取真实 ~/.walle。
// 全局状态：测试会临时清理 LLM_* 环境变量，并在 cleanup 恢复。
package utils

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadConfigWithOptionsSelectsDesignedProviders(t *testing.T) {
	isolateConfigEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".walle")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"default_auth":"chatgpt"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	deepseek, err := LoadConfigWithOptions(LoadConfigOptions{ModelRef: "deepseek/deepseek-flash"})
	if err != nil {
		t.Fatal(err)
	}
	if deepseek.LLM.Supplier != "deepseek" || deepseek.LLM.Provider != "openai" || deepseek.LLM.Model != "deepseek-flash" || deepseek.LLM.BaseURL != defaultDeepSeekBaseURL {
		t.Fatalf("deepseek config = %#v, want openai-compatible deepseek defaults", deepseek.LLM)
	}

	openaiChatGPT, err := LoadConfigWithOptions(LoadConfigOptions{ModelRef: "openai/gpt-5"})
	if err != nil {
		t.Fatal(err)
	}
	if openaiChatGPT.LLM.Supplier != "openai" || openaiChatGPT.LLM.Provider != "codex" || openaiChatGPT.LLM.Model != "gpt-5" || openaiChatGPT.LLM.APIKey != "" {
		t.Fatalf("openai chatgpt config = %#v, want codex without API key", openaiChatGPT.LLM)
	}

	codex, err := LoadConfigWithOptions(LoadConfigOptions{ModelRef: "codex/gpt-5"})
	if err != nil {
		t.Fatal(err)
	}
	if codex.LLM.Supplier != "codex" || codex.LLM.Provider != "codex" || codex.LLM.Model != "gpt-5" || codex.LLM.APIKey != "" {
		t.Fatalf("codex config = %#v, want codex provider without API key", codex.LLM)
	}
}

func TestConfiguredProvidersDiscoversEnvAndFileProviders(t *testing.T) {
	isolateConfigEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".walle")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envText := strings.Join([]string{
		"LLM_MODEL=zed/model",
		"LLM_AA_FORMAT=openai",
		"LLM_DEEPSEEK_FORMAT=openai",
	}, "\n")
	if err := os.WriteFile(filepath.Join(configDir, ".env"), []byte(envText), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLM_BAR_BAZ_FORMAT", "claude")

	providers, err := ConfiguredProviders()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(providers))
	for _, provider := range providers {
		got = append(got, provider.Name)
	}
	want := []string{"aa", "bar-baz", "deepseek", "zed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ConfiguredProviders() = %#v, want %#v", got, want)
	}
}

func TestLoadSystemPromptBaseAddsModelPrefixAndFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.md"), []byte("base prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prefixName := ModelPrefixPromptName("deepseek", "deepseek-flash") + ".md"
	if err := os.WriteFile(filepath.Join(dir, prefixName), []byte("model prefix\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadSystemPromptBase(dir, "tui", "deepseek", "deepseek-flash")
	if err != nil {
		t.Fatal(err)
	}
	if got != "model prefix\n\nbase prompt" {
		t.Fatalf("LoadSystemPromptBase() = %q, want prefix before fallback main", got)
	}
}

func isolateConfigEnv(t *testing.T) {
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
