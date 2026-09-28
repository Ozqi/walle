// Package utils 提供配置加载、prompt 读取、目录定位和 token 预算等通用能力。
package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/joho/godotenv"
)

// =============================================================================
// Prompt 加载
// =============================================================================

// Load 读取 <dir>/<name>.md prompt 文件并返回去除首尾空白后的内容。
func Load(dir, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, name+".md"))
	if err != nil {
		return "", fmt.Errorf("prompt %q not found: %w", name, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// LoadOptional 读取可选的 <dir>/<name>.md prompt 文件。
// 返回 ok=false 表示文件不存在；其他读取错误会返回给调用方处理。
func LoadOptional(dir, name string) (content string, ok bool, err error) {
	data, err := os.ReadFile(filepath.Join(dir, name+".md"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("prompt %q not readable: %w", name, err)
	}
	return strings.TrimSpace(string(data)), true, nil
}

// ModelPromptSlug 将 provider/model 编码成稳定的 prompt 文件名片段。
// 规则：小写；字母数字和 '-' 保留；其他字符折叠为单个 '-'。
func ModelPromptSlug(provider, model string) string {
	return promptSlug(provider) + "." + promptSlug(model)
}

// ModelPrefixPromptName 返回当前模型对应的可选 prefix prompt 名称。
func ModelPrefixPromptName(provider, model string) string {
	return "prefix." + ModelPromptSlug(provider, model)
}

// LoadSystemPromptBase 加载指定 base prompt，并在存在模型定制 prefix 时将其叠加到 base 前面。
// 优先级：指定 base -> 缺失时 main；模型 prefix 存在时位于 base 前。不同 Runtime 可使用不同 base。
func LoadSystemPromptBase(dir, base, provider, model string) (string, error) {
	if strings.TrimSpace(base) == "" {
		base = "main"
	}
	mainPrompt, err := Load(dir, base)
	if err != nil {
		if base != "main" && errors.Is(err, os.ErrNotExist) {
			mainPrompt, err = Load(dir, "main")
		}
		if err != nil {
			return "", err
		}
	}
	prefixName := ModelPrefixPromptName(provider, model)
	prefix, ok, err := LoadOptional(dir, prefixName)
	if err != nil {
		return "", err
	}
	if !ok || prefix == "" {
		return mainPrompt, nil
	}
	return prefix + "\n\n" + mainPrompt, nil
}

func promptSlug(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		keep := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if keep {
			b.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "unknown"
	}
	return slug
}

// =============================================================================
// 配置管理
// =============================================================================

// AppConfig 应用配置根结构。
type AppConfig struct {
	LLM   LLMConfig
	Agent AgentConfig
}

// LLMConfig LLM 提供商配置。
type LLMConfig struct {
	Supplier             string
	Provider             string // 接口格式：claude / openai / codex
	APIKey               string
	BaseURL              string
	Model                string
	MaxTokens            int
	ThinkingBudgetTokens int
	Stream               bool
}

// AgentConfig Agent 行为配置。
type AgentConfig struct {
	Name                string
	MaxTotalTokens      int
	RepeatToolLimit     int
	ContextAutoCompress bool
	Debug               bool
}

// LoadConfigOptions 描述运行期对 ~/.walle/.env 的覆盖。
// CLI 优先使用 ModelRef 完整切换 provider/model；LLMFormat/LLMModel 只覆盖当前 provider 的协议或模型。
type LoadConfigOptions struct {
	LLMFormat string
	LLMModel  string
	ModelRef  string
}

// ConfiguredProvider 是 ~/.walle/.env 中声明过的 provider 摘要。
type ConfiguredProvider struct {
	Name string
}

const (
	// DefaultProvider 是未指定接口协议时的默认值。
	DefaultProvider = "claude"
	// DefaultBaseURL 是默认 Claude API 地址。
	DefaultBaseURL = "https://api.anthropic.com"
	// DefaultModel 是默认模型名称。
	DefaultModel = "claude-sonnet-4-6"
	// DefaultMaxTokens 是单次生成的默认 token 上限。
	DefaultMaxTokens = 4096
	// DefaultAgentName 是默认 Agent 名称。
	DefaultAgentName = "walle"
	// DefaultMaxTotalTokens 是会话累计 token 的默认上限。
	DefaultMaxTotalTokens = 200000
	// DefaultRepeatToolLimit 是重复工具调用的默认限制。
	DefaultRepeatToolLimit = 5
	// DefaultContextAutoCompress 控制是否默认启用上下文自动压缩。
	DefaultContextAutoCompress = true
)

const (
	defaultOpenAIBaseURL   = "https://api.openai.com/v1"
	defaultDeepSeekBaseURL = "https://api.deepseek.com"
)

// GetConfigDir 返回 ~/.walle 目录路径。
func GetConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".walle"), nil
}

// GetProjectDataDir 返回项目数据目录 ./.walle/。
// Skill、hook 和运行日志等项目数据存储在此。
func GetProjectDataDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	return filepath.Join(cwd, ".walle"), nil
}

// LoadConfigWithOptions 加载运行配置，并应用运行期覆盖。
// 模型选择优先级：显式 opts.ModelRef -> settings.json default_model -> .env/进程环境 LLM_MODEL。
// 字段优先级：CLI LLMFormat/LLMModel -> 进程环境 -> ~/.walle/.env -> provider 默认值；FORMAT 表示 claude/openai/codex 接口协议。
func LoadConfigWithOptions(opts LoadConfigOptions) (*AppConfig, error) {
	// 1. 读取 .env 和 settings，确定本次模型引用及认证方式。
	config := defaultConfig()

	configDir, err := GetConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get config directory: %w", err)
	}
	envPath := filepath.Join(configDir, ".env")
	env, err := readEnvFile(envPath)
	if err != nil {
		return nil, err
	}
	settings, _ := loadSettings()
	if strings.TrimSpace(opts.ModelRef) == "" && settings.DefaultModel != "" {
		opts.ModelRef = settings.DefaultModel
	}
	// 2. ChatGPT 认证或 codex/ 模型引用强制选择 Codex OAuth 协议。
	if strings.HasPrefix(opts.ModelRef, "openai/") && settings.DefaultAuth == "chatgpt" {
		opts.LLMFormat = "codex"
	}
	if strings.TrimSpace(opts.LLMFormat) == "" && strings.HasPrefix(opts.ModelRef, "codex/") {
		opts.LLMFormat = "codex"
	}
	// 3. Codex 路径不读取 provider API key；其他路径再合并 provider 配置和 CLI 字段覆盖。
	if strings.TrimSpace(opts.ModelRef) != "" && strings.TrimSpace(opts.LLMFormat) == "codex" {
		if supplier, model, err := parseModelRef(opts.ModelRef); err == nil && (supplier == "openai" || supplier == "codex") {
			config.LLM = providerDefaults("codex", config.LLM)
			config.LLM.Supplier = supplier
			config.LLM.Model = model
			loadAgentConfig(env, &config.Agent)
			if err := config.Validate(); err != nil {
				return nil, err
			}
			return config, nil
		}
	}

	config.LLM, err = loadLLMConfig(env, config.LLM, opts)
	if err != nil {
		return nil, err
	}
	loadAgentConfig(env, &config.Agent)

	// 4. Agent 配置与当前生效的单个 LLM provider 一并校验。
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return config, nil
}

// ConfiguredProviders 返回 LLM_MODEL 和 LLM_<PROVIDER>_FORMAT 声明过的 provider，并去重排序。
// 进程环境中的同名键与 ~/.walle/.env 都参与发现，但该函数不校验配置完整性。
func ConfiguredProviders() ([]ConfiguredProvider, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return nil, err
	}
	env, err := readEnvFile(filepath.Join(configDir, ".env"))
	if err != nil {
		return nil, err
	}
	providers := map[string]string{}
	if supplier, _, err := parseModelRef(getEnvValue(env, "LLM_MODEL", "")); err == nil && supplier != "" {
		providers[strings.ToLower(supplier)] = supplier
	}
	keys := make([]string, 0, len(env)+len(os.Environ()))
	for key := range env {
		keys = append(keys, key)
	}
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, "LLM_") || !strings.HasSuffix(key, "_FORMAT") {
			continue
		}
		provider := strings.TrimSuffix(strings.TrimPrefix(key, "LLM_"), "_FORMAT")
		provider = strings.ToLower(strings.ReplaceAll(strings.Trim(provider, "_"), "_", "-"))
		if provider == "" {
			continue
		}
		if _, exists := providers[provider]; !exists {
			providers[provider] = provider
		}
	}
	list := make([]ConfiguredProvider, 0, len(providers))
	for _, name := range providers {
		list = append(list, ConfiguredProvider{Name: name})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

type settingsFile struct {
	DefaultModel string `json:"default_model"`
	DefaultAuth  string `json:"default_auth,omitempty"`
}

func loadSettings() (settingsFile, error) {
	var settings settingsFile
	dir, err := GetConfigDir()
	if err != nil {
		return settings, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return settings, err
	}
	settings.DefaultModel = strings.TrimSpace(settings.DefaultModel)
	settings.DefaultAuth = strings.TrimSpace(settings.DefaultAuth)
	return settings, nil
}

func readEnvFile(path string) (map[string]string, error) {
	env, err := godotenv.Read(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("failed to load .env: %w", err)
	}
	return env, nil
}

func loadLLMConfig(env map[string]string, defaults LLMConfig, opts LoadConfigOptions) (LLMConfig, error) {
	// ModelRef 显式值优先于 LLM_MODEL；选定 supplier 后只加载对应配置块。
	modelRef := strings.TrimSpace(opts.ModelRef)
	if modelRef == "" {
		modelRef = strings.TrimSpace(getEnvValue(env, "LLM_MODEL", ""))
	}
	refSupplier, refModel, err := parseModelRef(modelRef)
	if err != nil {
		return LLMConfig{}, err
	}
	if refSupplier == "" || refModel == "" {
		return LLMConfig{}, fmt.Errorf("LLM_MODEL is required and must use provider/model format; edit ~/.walle/.env or run with --model <provider>/<model>")
	}
	cfg, err := loadProviderConfig(env, refSupplier, defaults)
	if err != nil {
		return LLMConfig{}, err
	}
	cfg.Model = refModel
	return applyLLMOverrides(cfg, opts), nil
}

func parseModelRef(ref string) (supplier string, model string, err error) {
	if ref == "" {
		return "", "", nil
	}
	before, after, ok := strings.Cut(ref, "/")
	if !ok || before == "" || after == "" {
		return "", "", fmt.Errorf("LLM_MODEL must use provider/model format, got %q; example: mygateway/<model>", ref)
	}
	return before, after, nil
}

func loadProviderConfig(env map[string]string, supplier string, defaults LLMConfig) (LLMConfig, error) {
	// 先按接口格式建立默认值，再由进程环境优先、.env 次之地覆盖 provider 字段。
	prefix := supplierEnvPrefix(supplier)
	format := strings.ToLower(getEnvValue(env, prefix+"_FORMAT", ""))
	if supplier == "codex" && format == "" {
		format = "codex"
	}
	if supplier == "deepseek" && format == "" {
		format = "openai"
	}
	if format == "" {
		return LLMConfig{}, fmt.Errorf("%s_FORMAT is required for LLM provider %q", prefix, supplier)
	}
	cfg := providerDefaults(format, defaults)
	cfg.Supplier = supplier
	cfg.Provider = format
	if supplier == "deepseek" && format == "openai" {
		cfg.BaseURL = defaultDeepSeekBaseURL
		cfg.APIKey = getEnvValue(env, "DEEPSEEK_API_KEY", cfg.APIKey)
	}
	cfg.APIKey = getEnvValue(env, prefix+"_API_KEY", cfg.APIKey)
	cfg.BaseURL = getEnvValue(env, prefix+"_BASE_URL", cfg.BaseURL)
	if maxTokens := getEnvValue(env, prefix+"_MAX_TOKENS", ""); maxTokens != "" {
		if v, err := strconv.Atoi(maxTokens); err == nil {
			cfg.MaxTokens = v
		}
	}
	if budget := getEnvValue(env, prefix+"_THINKING_BUDGET_TOKENS", ""); budget != "" {
		if v, err := strconv.Atoi(budget); err == nil {
			cfg.ThinkingBudgetTokens = v
		}
	}
	if stream := getEnvValue(env, prefix+"_STREAM", ""); stream != "" {
		cfg.Stream = strings.ToLower(stream) != "false"
	}
	return cfg, nil
}

func applyLLMOverrides(cfg LLMConfig, opts LoadConfigOptions) LLMConfig {
	// CLI 协议覆盖会先切换协议默认值，随后模型名覆盖最终生效模型。
	if format := strings.ToLower(strings.TrimSpace(opts.LLMFormat)); format != "" {
		cfg.Provider = format
		cfg = applyProviderDefaults(cfg, defaultConfig().LLM)
	}
	if model := strings.TrimSpace(opts.LLMModel); model != "" {
		cfg.Model = model
	}
	return cfg
}

func providerDefaults(format string, defaults LLMConfig) LLMConfig {
	cfg := defaults
	cfg.Provider = format
	if format == "openai" {
		cfg.BaseURL = defaultOpenAIBaseURL
		cfg.Model = ""
		cfg.ThinkingBudgetTokens = 0
	}
	if format == "codex" {
		cfg.BaseURL = "https://chatgpt.com/backend-api/codex"
		cfg.Model = ""
		cfg.APIKey = ""
		cfg.ThinkingBudgetTokens = 0
	}
	return cfg
}

func applyProviderDefaults(cfg LLMConfig, defaults LLMConfig) LLMConfig {
	withDefaults := providerDefaults(cfg.Provider, defaults)
	if cfg.BaseURL == "" || cfg.BaseURL == defaults.BaseURL || (withDefaults.Provider == "openai" && cfg.BaseURL == defaultOpenAIBaseURL) {
		cfg.BaseURL = withDefaults.BaseURL
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = withDefaults.MaxTokens
	}
	if cfg.Model == "" {
		cfg.Model = withDefaults.Model
	}
	return cfg
}

func supplierEnvPrefix(supplier string) string {
	var b strings.Builder
	b.WriteString("LLM_")
	lastUnderscore := false
	for _, r := range supplier {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r - 'a' + 'A')
			lastUnderscore = false
			continue
		}
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.TrimRight(b.String(), "_")
}

func loadAgentConfig(env map[string]string, config *AgentConfig) {
	config.Name = getEnvValue(env, "AGENT_NAME", config.Name)
	if maxTotal := getEnvValue(env, "AGENT_MAX_TOTAL_TOKENS", ""); maxTotal != "" {
		if v, err := strconv.Atoi(maxTotal); err == nil {
			config.MaxTotalTokens = v
		}
	}
	if repeat := getEnvValue(env, "AGENT_REPEAT_TOOL_LIMIT", ""); repeat != "" {
		if v, err := strconv.Atoi(repeat); err == nil {
			config.RepeatToolLimit = v
		}
	}
	if auto := getEnvValue(env, "AGENT_CONTEXT_AUTO_COMPRESS", ""); auto != "" {
		config.ContextAutoCompress = strings.ToLower(auto) != "false"
	}
}

func getEnvValue(env map[string]string, key, fallback string) string {
	// 配置字段优先读取非空进程环境值，其次读取非空 .env 值，最后使用默认值。
	if v := os.Getenv(key); v != "" {
		return v
	}
	if v := env[key]; v != "" {
		return v
	}
	return fallback
}

func defaultConfig() *AppConfig {
	return &AppConfig{
		LLM: LLMConfig{
			Provider:  DefaultProvider,
			BaseURL:   DefaultBaseURL,
			Model:     DefaultModel,
			MaxTokens: DefaultMaxTokens,
			Stream:    true,
		},
		Agent: AgentConfig{
			Name:                DefaultAgentName,
			MaxTotalTokens:      DefaultMaxTotalTokens,
			RepeatToolLimit:     DefaultRepeatToolLimit,
			ContextAutoCompress: DefaultContextAutoCompress,
			Debug:               false,
		},
	}
}

// Validate 验证当前生效的 LLM provider 和 Agent 数值配置。
func (c *AppConfig) Validate() error {
	if err := validateLLMConfig(c.LLM); err != nil {
		return err
	}
	if c.Agent.MaxTotalTokens <= 0 {
		return fmt.Errorf("AGENT_MAX_TOTAL_TOKENS must be positive")
	}
	if c.Agent.RepeatToolLimit <= 0 {
		return fmt.Errorf("AGENT_REPEAT_TOOL_LIMIT must be positive")
	}
	return nil
}

func validateLLMConfig(config LLMConfig) error {
	switch config.Provider {
	case "claude":
		if config.APIKey == "" {
			return fmt.Errorf("%s is required for claude format. Please set in ~/.walle/.env", llmEnvKey(config, "API_KEY"))
		}
	case "openai":
	// OpenAI-compatible 本地服务可使用 dummy key；远端服务按上游要求填写。
	case "codex":
		// Codex 使用用户级 OAuth store，不从 .env 读取 API key。
	default:
		return fmt.Errorf("unsupported LLM format %q, supported: claude, openai, codex", config.Provider)
	}
	if config.BaseURL == "" {
		return fmt.Errorf("%s is required", llmEnvKey(config, "BASE_URL"))
	}
	if config.Model == "" {
		return fmt.Errorf("%s is required", llmEnvKey(config, "MODEL"))
	}
	if config.MaxTokens <= 0 {
		return fmt.Errorf("%s must be positive", llmEnvKey(config, "MAX_TOKENS"))
	}
	if config.ThinkingBudgetTokens < 0 {
		return fmt.Errorf("%s must be non-negative", llmEnvKey(config, "THINKING_BUDGET_TOKENS"))
	}
	return nil
}

func llmEnvKey(config LLMConfig, field string) string {
	if config.Supplier != "" {
		return supplierEnvPrefix(config.Supplier) + "_" + field
	}
	return "LLM_" + strings.ToUpper(config.Provider) + "_" + field
}
