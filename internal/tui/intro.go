// 本文件负责 TUI 开头条目的只读摘要。
// 调用方：AppModel.Init 异步加载，app_view.go 渲染 session 第一屏。
// 全局状态：无；只读取 ~/.walle/prompt 和 skill 目录，不参与 Runtime 真源。
package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Ozqi/walle/internal/utils"
	tea "github.com/charmbracelet/bubbletea"
)

type introInfo struct {
	Prompts []string
	Skills  []string
}

type introLoadedMsg struct{ info introInfo }

// loadIntroCmd 汇总启动时通常会注入的 prompt 与 skill 名称，只用于开头页展示。
func (m *AppModel) loadIntroCmd() tea.Cmd {
	workdir := m.metaCache.Workdir
	if workdir == "" || workdir == "-" {
		if cwd, err := os.Getwd(); err == nil {
			workdir = cwd
		}
	}
	model := m.modelName
	return func() tea.Msg {
		return introLoadedMsg{info: introInfo{Prompts: introPrompts(model), Skills: introSkills(workdir)}}
	}
}

func introPrompts(model string) []string {
	configDir, err := utils.GetConfigDir()
	if err != nil {
		return []string{"main.md"}
	}
	promptDir := filepath.Join(configDir, "prompt")
	prompts := []string{"main.md"}
	provider, name, ok := strings.Cut(model, "/")
	if !ok || strings.TrimSpace(provider) == "" || strings.TrimSpace(name) == "" {
		return prompts
	}
	prefix := utils.ModelPrefixPromptName(provider, name) + ".md"
	if _, err := os.Stat(filepath.Join(promptDir, prefix)); err == nil {
		prompts = append(prompts, prefix)
	}
	return prompts
}

func introSkills(workdir string) []string {
	configDir, err := utils.GetConfigDir()
	if err != nil {
		return nil
	}
	sources := []string{filepath.Join(configDir, "skills")}
	if strings.TrimSpace(workdir) != "" && workdir != "-" {
		sources = append(sources, filepath.Join(workdir, ".walle", "skills"))
	}
	seen := map[string]string{}
	for _, dir := range sources {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name(), "SKILL.md")
			if _, err := os.Stat(path); err != nil {
				continue
			}
			name := readSkillTitle(path, entry.Name())
			seen[name] = name
		}
	}
	items := make([]string, 0, len(seen))
	for _, item := range seen {
		items = append(items, item)
	}
	sort.Strings(items)
	return items
}

func readSkillTitle(path string, fallbackName string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fallbackName
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "---" || line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			break
		}
		if strings.TrimSpace(key) == "name" {
			if val = strings.Trim(strings.TrimSpace(val), `"'`); val != "" {
				return val
			}
		}
	}
	return fallbackName
}
