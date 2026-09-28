// 功能：验证 MCP stdio 客户端能启动真实子进程、发现工具并完成 tools/call。
// 调用方：go test ./internal/mcp；测试进程用环境变量切到 MCP helper 模式作为外部 stdio server。
// 全局状态：无；只启动当前测试二进制的子进程，不读写用户级 MCP 配置。
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("WALLE_MCP_HELPER") == "1" {
		runMCPHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestStdioClientCallsExternalTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := NewStdioClient(ctx, StdioClientConfig{
		Name:           "demo",
		Command:        os.Args[0],
		Env:            map[string]string{"WALLE_MCP_HELPER": "1"},
		StartupTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	tools := client.ListTools()
	if len(tools) != 1 || tools[0].Name != "lookup" {
		t.Fatalf("ListTools() = %#v, want lookup tool", tools)
	}

	got, err := client.CallTool(ctx, "lookup", `{"query":"wall-e"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "mcp ok: wall-e" {
		t.Fatalf("CallTool() = %q, want mcp ok", got)
	}
}

func runMCPHelper() {
	reader := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for reader.Scan() {
		var req jsonrpcRequest
		if err := json.Unmarshal(reader.Bytes(), &req); err != nil {
			continue
		}
		if req.ID == nil {
			continue
		}
		response := mcpHelperResponse(req)
		_ = encoder.Encode(response)
	}
}

func mcpHelperResponse(req jsonrpcRequest) jsonrpcResponse {
	switch req.Method {
	case "initialize":
		return mcpHelperResult(req.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]bool{"listChanged": false}},
			"serverInfo":      map[string]string{"name": "walle-test-mcp", "version": "0.0.0"},
		})
	case "tools/list":
		return mcpHelperResult(req.ID, map[string]interface{}{
			"tools": []map[string]interface{}{{
				"name":        "lookup",
				"description": "return the query text",
				"inputSchema": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{"query": map[string]string{"type": "string"}},
				},
			}},
		})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &params)
		var args struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(params.Arguments, &args)
		return mcpHelperResult(req.ID, map[string]interface{}{
			"content": []map[string]string{{"type": "text", "text": fmt.Sprintf("mcp ok: %s", args.Query)}},
		})
	default:
		return jsonrpcResponse{Jsonrpc: "2.0", ID: req.ID, Error: &jsonrpcError{Code: -32601, Message: "method not found"}}
	}
}

func mcpHelperResult(id interface{}, result interface{}) jsonrpcResponse {
	data, _ := json.Marshal(result)
	return jsonrpcResponse{Jsonrpc: "2.0", ID: id, Result: data}
}
