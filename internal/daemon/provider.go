package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Ozqi/walle/internal/codex"
	agentrt "github.com/Ozqi/walle/internal/runtime"
	"github.com/Ozqi/walle/internal/utils"
)

type providerInfo struct {
	Name     string
	LoggedIn bool
}

func providers(rt *agentrt.Runtime) []providerInfo {
	current, _, _ := strings.Cut(rt.ModelRef, "/")
	store, _ := codex.DefaultStore()
	seen := map[string]bool{"openai": true, "deepseek": true}
	result := []providerInfo{{Name: "openai", LoggedIn: store != nil && store.LoggedIn()}, {Name: "deepseek", LoggedIn: true}}
	if current != "" && !seen[strings.ToLower(current)] {
		seen[strings.ToLower(current)] = true
		result = append(result, providerInfo{Name: current, LoggedIn: true})
	}
	if configured, err := utils.ConfiguredProviders(); err == nil {
		for _, provider := range configured {
			key := strings.ToLower(provider.Name)
			if provider.Name != "" && !seen[key] {
				seen[key] = true
				result = append(result, providerInfo{Name: provider.Name, LoggedIn: true})
			}
		}
	}
	sort.SliceStable(result[1:], func(i, j int) bool { return result[1+i].Name < result[1+j].Name })
	return result
}

var startOpenAILoginFunc = startOpenAILogin
var providerModelsFunc = providerModels

func startOpenAILogin(ctx context.Context) (string, <-chan error, error) {
	store, err := codex.DefaultStore()
	if err != nil {
		return "", nil, err
	}
	return store.StartLogin(ctx)
}

func providerModels(ctx context.Context, provider string) ([]string, error) {
	if provider == "openai" {
		store, err := codex.DefaultStore()
		if err != nil {
			return nil, err
		}
		return store.Models(ctx)
	}
	config, err := utils.LoadConfigWithOptions(utils.LoadConfigOptions{ModelRef: provider + "/__model_catalog__"})
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(strings.TrimRight(config.LLM.BaseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid provider base url %q", config.LLM.BaseURL)
	}
	if config.LLM.Provider == "claude" && !strings.HasSuffix(parsed.Path, "/v1") {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/models"
	parsed.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	if config.LLM.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+config.LLM.APIKey)
	}
	if config.LLM.Provider == "claude" {
		request.Header.Set("x-api-key", config.LLM.APIKey)
		request.Header.Set("anthropic-version", "2023-06-01")
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("list %s models: %w", provider, err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return nil, fmt.Errorf("list %s models: status %s", provider, response.Status)
	}
	var payload struct {
		Data   []modelCatalogItem `json:"data"`
		Models []modelCatalogItem `json:"models"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("list %s models: %w", provider, err)
	}
	seen := map[string]bool{}
	var models []string
	for _, item := range append(payload.Data, payload.Models...) {
		for _, value := range []string{item.ID, item.Name, item.Model, item.Slug} {
			name := strings.TrimSpace(value)
			if name != "" {
				if !seen[name] {
					seen[name] = true
					models = append(models, name)
				}
				break
			}
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("list %s models: empty catalog", provider)
	}
	sort.Strings(models)
	return models, nil
}

type modelCatalogItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Model string `json:"model"`
	Slug  string `json:"slug"`
}
