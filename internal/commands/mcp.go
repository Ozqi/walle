package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ozqi/walle/internal/mcp"
)

type mcpServerEntry struct {
	Name           string            `json:"name"`
	Command        string            `json:"command"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	StartupTimeout time.Duration     `json:"startup_timeout,omitempty"`
	Enabled        bool              `json:"enabled"`
}

type mcpConfigFile struct {
	Servers []mcpServerEntry `json:"servers"`
}

// HandleMCP 校验并分派 /mcp 子命令；修改类命令会覆盖用户级 mcp.json。
func HandleMCP(cmd string) (string, error) {
	parts := strings.Fields(cmd)
	if len(parts) < 2 {
		return "", fmt.Errorf("usage: /mcp <list|add|remove|enable|disable> [args...]")
	}

	action := parts[1]
	switch action {
	case "list":
		return listMCPServers()
	case "add":
		if len(parts) < 4 {
			return "", fmt.Errorf("usage: /mcp add <name> <command> [args...]")
		}
		return addMCPServer(parts[2], parts[3], parts[4:])
	case "remove":
		if len(parts) < 3 {
			return "", fmt.Errorf("usage: /mcp remove <name>")
		}
		return removeMCPServer(parts[2])
	case "enable":
		if len(parts) < 3 {
			return "", fmt.Errorf("usage: /mcp enable <name>")
		}
		return enableMCPServer(parts[2])
	case "disable":
		if len(parts) < 3 {
			return "", fmt.Errorf("usage: /mcp disable <name>")
		}
		return disableMCPServer(parts[2])
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func listMCPServers() (string, error) {
	config, err := loadMCPConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}

	if len(config.Servers) == 0 {
		return "No MCP servers configured. Add one with /mcp add <name> <command> [args...]", nil
	}

	var sb strings.Builder
	sb.WriteString("MCP Servers:\n")
	for _, s := range config.Servers {
		status := "disabled"
		if s.Enabled {
			status = "enabled"
		}
		fullName := mcp.FullToolName(s.Name, "*")
		sb.WriteString(fmt.Sprintf("  - %s [%s]: %s %v\n", fullName, status, s.Command, s.Args))
	}
	return sb.String(), nil
}

func addMCPServer(name string, command string, args []string) (string, error) {
	// 复用 MCP 包的 server 配置校验，避免命令层接受运行时无法注册的名称。
	if err := (mcp.ServerConfig{Name: name, Command: command}).Validate(); err != nil {
		return "", err
	}
	if strings.HasPrefix(name, "mcp.") {
		return "", fmt.Errorf("name cannot start with 'mcp.'")
	}

	config, err := loadMCPConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}

	for _, s := range config.Servers {
		if s.Name == name {
			return "", fmt.Errorf("server '%s' already exists", name)
		}
	}

	newServer := mcpServerEntry{
		Name:    name,
		Command: command,
		Args:    args,
		Enabled: true,
	}
	config.Servers = append(config.Servers, newServer)

	if err := saveMCPConfig(config); err != nil {
		return "", fmt.Errorf("failed to save config: %w", err)
	}

	return fmt.Sprintf("MCP server '%s' added and enabled (restart required to activate)", name), nil
}

func removeMCPServer(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("server name is required")
	}

	config, err := loadMCPConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}

	found := false
	newServers := make([]mcpServerEntry, 0, len(config.Servers))
	for _, s := range config.Servers {
		if s.Name == name {
			found = true
			continue
		}
		newServers = append(newServers, s)
	}

	if !found {
		return "", fmt.Errorf("server '%s' not found", name)
	}

	config.Servers = newServers
	if err := saveMCPConfig(config); err != nil {
		return "", fmt.Errorf("failed to save config: %w", err)
	}

	return fmt.Sprintf("MCP server '%s' removed (restart required)", name), nil
}

func enableMCPServer(name string) (string, error) {
	return setMCPServerEnabled(name, true)
}

func disableMCPServer(name string) (string, error) {
	return setMCPServerEnabled(name, false)
}

func setMCPServerEnabled(name string, enabled bool) (string, error) {
	if name == "" {
		return "", fmt.Errorf("server name is required")
	}

	config, err := loadMCPConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}

	state := "disabled"
	result := fmt.Sprintf("MCP server '%s' disabled (restart required)", name)
	if enabled {
		state = "enabled"
		result = fmt.Sprintf("MCP server '%s' enabled (restart required to activate)", name)
	}

	for i, s := range config.Servers {
		if s.Name != name {
			continue
		}
		if config.Servers[i].Enabled == enabled {
			return fmt.Sprintf("MCP server '%s' is already %s", name, state), nil
		}
		config.Servers[i].Enabled = enabled
		if err := saveMCPConfig(config); err != nil {
			return "", fmt.Errorf("failed to save config: %w", err)
		}
		return result, nil
	}

	return "", fmt.Errorf("server '%s' not found", name)
}

func loadMCPConfig() (*mcpConfigFile, error) {
	configPath, err := mcpConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", configPath, err)
	}

	var cfg mcpConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", configPath, err)
	}

	return &cfg, nil
}

func saveMCPConfig(cfg *mcpConfigFile) error {
	// 配置按“创建目录 -> 序列化完整快照 -> 覆盖文件”的顺序写入，不修改运行中的 MCP 客户端。
	configPath, err := mcpConfigPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(configPath, data, 0644)
}

func mcpConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".walle", "mcp.json"), nil
}
