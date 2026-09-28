// 功能：验证 daemon HTTP JSON、SSE 和 WebSocket transport 的协议语义。
// 调用方：go test ./internal/daemon；使用最小 DaemonSession，避免真实 LLM 请求。
// 全局状态：无；每个测试独立创建 httptest.Server 和 Registry。
package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ozqi/walle/internal/agent"
	agentrt "github.com/Ozqi/walle/internal/runtime"
	"golang.org/x/net/websocket"
)

func TestHTTPGatewayAuthAndProcesses(t *testing.T) {
	server := newHTTPGatewayTestServer("secret")
	defer server.Close()

	if status := httpGetStatus(t, server.URL+"/v1/processes", ""); status != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", status)
	}
	if status := httpGetStatus(t, server.URL+"/v1/processes", "wrong"); status != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", status)
	}
	resp, err := httpJSONRequest(t, http.MethodGet, server.URL+"/v1/processes", "secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var processes []ProcessSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&processes); err != nil {
		t.Fatal(err)
	}
	if len(processes) != 1 || processes[0].ID != "interactive-1" {
		t.Fatalf("unexpected processes: %#v", processes)
	}
}

func TestHTTPGatewaySnapshotInputStop(t *testing.T) {
	server := newHTTPGatewayTestServer("secret")
	defer server.Close()

	resp, err := httpJSONRequest(t, http.MethodGet, server.URL+"/v1/runtimes/interactive-1", "secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status = %d", resp.StatusCode)
	}
	var snapshot ProcessSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ID != "interactive-1" || snapshot.SessionID != "session-1" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}

	resp, err = httpJSONRequest(t, http.MethodPost, server.URL+"/v1/runtimes/interactive-1/input", "secret", bytes.NewBufferString(`{"text":"/session"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("input status = %d", resp.StatusCode)
	}
	resp, err = httpJSONRequest(t, http.MethodPost, server.URL+"/v1/runtimes/interactive-1/stop", "secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop status = %d", resp.StatusCode)
	}
}

func TestHTTPGatewayOrigin(t *testing.T) {
	server := newHTTPGatewayTestServer("secret")
	defer server.Close()

	if status := httpGetStatus(t, server.URL+"/v1/processes", "secret"); status != http.StatusOK {
		t.Fatalf("no origin status = %d", status)
	}
	origin := strings.Replace(server.URL, "http://127.0.0.1", "http://localhost", 1)
	if status := httpGetStatusWithOrigin(t, server.URL+"/v1/processes", "secret", origin); status != http.StatusOK {
		t.Fatalf("localhost origin status = %d", status)
	}
	if status := httpGetStatusWithOrigin(t, server.URL+"/v1/processes", "secret", "https://example.com"); status != http.StatusForbidden {
		t.Fatalf("foreign origin status = %d", status)
	}
}

func TestHTTPGatewaySSE(t *testing.T) {
	server := newHTTPGatewayTestServer("secret")
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/runtimes/interactive-1/events?token=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content type = %q", got)
	}
	reader := bufio.NewReader(resp.Body)
	if line := readSSELine(t, reader); line != "event: attached" {
		t.Fatalf("first sse line = %q", line)
	}
	readSSELine(t, reader)
	readSSELine(t, reader)
	if line := readSSELine(t, reader); line != "event: ready" {
		t.Fatalf("ready line = %q", line)
	}
}

func TestHTTPGatewayWebSocket(t *testing.T) {
	server := newHTTPGatewayTestServer("secret")
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/runtimes/interactive-1/attach?token=secret"
	conn, err := websocket.Dial(url, "", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(time.Second)
	if msg := receiveWS(t, conn, deadline); msg.Type != "attached" || msg.Process == nil {
		t.Fatalf("attached = %#v", msg)
	}
	if msg := receiveWS(t, conn, deadline); msg.Type != "ready" {
		t.Fatalf("ready = %#v", msg)
	}
	sendWS(t, conn, controlMessage{Type: "input", ID: 11, Text: "/session"})
	if msg := receiveWSUntil(t, conn, "input_result"); msg.ID != 11 || msg.Error != "" {
		t.Fatalf("input result = %#v", msg)
	}
	sendWS(t, conn, controlMessage{Type: "stop", ID: 12})
	if msg := receiveWSUntil(t, conn, "stop_result"); msg.ID != 12 || msg.Error != "" {
		t.Fatalf("stop result = %#v", msg)
	}
	sendWS(t, conn, controlMessage{Type: "detach"})
}

func newHTTPGatewayTestServer(token string) *httptest.Server {
	registry := NewRegistry(context.Background())
	registry.sessions["interactive-1"] = NewDaemonSession(context.Background(), &agentrt.Runtime{
		Agent:      &agent.Agent{},
		ProjectDir: "/tmp/workspace",
		ModelRef:   "local/test",
		SessionID:  "session-1",
	}, "interactive-1", "workspace")
	h := &httpGatewayHandler{ctx: context.Background(), registry: registry, token: token}
	server := httptest.NewServer(h)
	h.addr = strings.TrimPrefix(server.URL, "http://")
	return server
}

func httpGetStatus(t *testing.T, url string, token string) int {
	t.Helper()
	resp, err := httpJSONRequest(t, http.MethodGet, url, token, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func httpGetStatusWithOrigin(t *testing.T, url string, token string, origin string) int {
	t.Helper()
	resp, err := httpJSONRequest(t, http.MethodGet, url, token, nil, map[string]string{"Origin": origin})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func httpJSONRequest(t *testing.T, method string, url string, token string, body *bytes.Buffer, headers map[string]string) (*http.Response, error) {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body.Bytes())
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return http.DefaultClient.Do(req)
}

func readSSELine(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read sse line: %v", err)
	}
	return strings.TrimRight(line, "\r\n")
}

func sendWS(t *testing.T, conn *websocket.Conn, msg controlMessage) {
	t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := websocket.Message.Send(conn, string(data)); err != nil {
		t.Fatal(err)
	}
}

func receiveWSUntil(t *testing.T, conn *websocket.Conn, kind string) controlMessage {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		msg := receiveWS(t, conn, deadline)
		if msg.Type == kind {
			return msg
		}
	}
}

func receiveWS(t *testing.T, conn *websocket.Conn, deadline time.Time) controlMessage {
	t.Helper()
	_ = conn.SetReadDeadline(deadline)
	defer conn.SetReadDeadline(time.Time{})
	var data string
	if err := websocket.Message.Receive(conn, &data); err != nil {
		t.Fatal(err)
	}
	var msg controlMessage
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		t.Fatal(err)
	}
	return msg
}
