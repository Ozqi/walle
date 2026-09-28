// 功能：提供 daemon 的 localhost HTTP、WebSocket 和 SSE 传输适配。
// 调用方：cmd/walle daemon 在显式开启 --http 时启动；测试可用 httptest 直接挂载。
// 全局状态：无；HTTPServer 持有 server 生命周期，token 由调用方或 runDir 文件提供。
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

const defaultHTTPAddr = "127.0.0.1:0"

var errRuntimeNotFound = errors.New("runtime not found")

// HTTPOptions 控制本机 HTTP/WebSocket/SSE Gateway 的监听和访问边界。
type HTTPOptions struct {
	Addr             string   // 监听地址，默认 127.0.0.1:0
	Token            string   // 访问 token；为空时从 runDir/http_token 读取或创建
	AllowedOrigins   []string // 额外允许的浏览器 Origin
	AllowNonLoopback bool     // 是否允许监听非 loopback 地址
}

// HTTPServer 是 HTTP/WebSocket/SSE transport 的生命周期句柄。
type HTTPServer struct {
	server *http.Server
	addr   string
}

// StartHTTPServer 启动本机 HTTP Gateway，并把请求转成 Registry / DaemonSession 操作。
// 参数：runDir 保存 http_token；registry 提供 Runtime 列表、打开和查找。
// 副作用：监听 TCP 端口，必要时写入 runDir/http_token。
func StartHTTPServer(ctx context.Context, runDir string, registry *Registry, opts HTTPOptions) (*HTTPServer, error) {
	addr := strings.TrimSpace(opts.Addr)
	if addr == "" {
		addr = defaultHTTPAddr
	}
	if err := ValidateLoopbackAddr(addr, opts.AllowNonLoopback); err != nil {
		return nil, err
	}
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		var err error
		token, err = LoadOrCreateHTTPToken(runDir)
		if err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen http gateway: %w", err)
	}
	h := &httpGatewayHandler{ctx: ctx, registry: registry, token: token, origins: opts.AllowedOrigins, addr: ln.Addr().String()}
	server := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	gateway := &HTTPServer{server: server, addr: ln.Addr().String()}
	go func() {
		<-ctx.Done()
		_ = gateway.Close()
	}()
	go func() { _ = server.Serve(ln) }()
	return gateway, nil
}

// Addr 返回 HTTP Gateway 实际监听地址。
func (s *HTTPServer) Addr() string {
	if s == nil {
		return ""
	}
	return s.addr
}

// Close 关闭 HTTP Gateway。
func (s *HTTPServer) Close() error {
	if s == nil || s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}

type httpGatewayHandler struct {
	ctx      context.Context
	registry *Registry
	token    string
	origins  []string
	addr     string
}

type httpOK struct {
	OK bool `json:"ok"`
}

type httpError struct {
	Error string `json:"error"`
}

type inputRequest struct {
	Text string `json:"text"`
}

func (h *httpGatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !CheckHTTPToken(r, h.token) {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !CheckOrigin(r, h.origins, h.addr) {
		h.writeError(w, http.StatusForbidden, "origin is not allowed")
		return
	}
	if r.URL.Path == "/v1/processes" && r.Method == http.MethodGet {
		h.writeJSON(w, http.StatusOK, h.registry.list())
		return
	}
	if r.URL.Path == "/v1/runtimes/open" && r.Method == http.MethodPost {
		h.handleOpen(w, r)
		return
	}
	id, action, ok := parseRuntimePath(r.URL.Path)
	if !ok {
		h.writeError(w, http.StatusNotFound, "not found")
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		h.handleSnapshot(w, id)
	case action == "input" && r.Method == http.MethodPost:
		h.handleInput(w, r, id)
	case action == "stop" && r.Method == http.MethodPost:
		h.handleStop(w, id)
	case action == "events" && r.Method == http.MethodGet:
		h.handleSSE(w, r, id)
	case action == "attach" && r.Method == http.MethodGet:
		h.handleWebSocket(w, r, id)
	default:
		h.writeError(w, http.StatusNotFound, "not found")
	}
}

func parseRuntimePath(path string) (id string, action string, ok bool) {
	rest := strings.TrimPrefix(path, "/v1/runtimes/")
	if rest == path || rest == "" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 1 && parts[0] != "" {
		return parts[0], "", true
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func (h *httpGatewayHandler) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req OpenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode open request: %v", err))
		return
	}
	target, err := h.registry.open(h.ctx, req)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, target.Snapshot())
}

func (h *httpGatewayHandler) handleSnapshot(w http.ResponseWriter, id string) {
	target, err := h.find(id)
	if err != nil {
		h.writeError(w, statusForGatewayError(err), err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, target.Snapshot())
}

func (h *httpGatewayHandler) handleInput(w http.ResponseWriter, r *http.Request, id string) {
	var req inputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode input request: %v", err))
		return
	}
	target, err := h.find(id)
	if err != nil {
		h.writeError(w, statusForGatewayError(err), err.Error())
		return
	}
	if err := target.Submit(req.Text); err != nil {
		h.writeError(w, statusForGatewayError(err), err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, httpOK{OK: true})
}

func (h *httpGatewayHandler) handleStop(w http.ResponseWriter, id string) {
	target, err := h.find(id)
	if err != nil {
		h.writeError(w, statusForGatewayError(err), err.Error())
		return
	}
	if err := target.Stop(); err != nil {
		h.writeError(w, statusForGatewayError(err), err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, httpOK{OK: true})
}

func (h *httpGatewayHandler) handleSSE(w http.ResponseWriter, r *http.Request, id string) {
	target, err := h.find(id)
	if err != nil {
		h.writeError(w, statusForGatewayError(err), err.Error())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	history, events, detach := target.Attach()
	defer detach()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if !writeSSE(w, flusher, "attached", target.Snapshot()) {
		return
	}
	for i := range history {
		if !writeSSE(w, flusher, "event", history[i]) {
			return
		}
	}
	if !writeSSE(w, flusher, "ready", map[string]bool{}) {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok || !writeSSE(w, flusher, "event", event) {
				return
			}
		}
	}
}

func (h *httpGatewayHandler) handleWebSocket(w http.ResponseWriter, r *http.Request, id string) {
	target, err := h.find(id)
	if err != nil {
		h.writeError(w, statusForGatewayError(err), err.Error())
		return
	}
	server := websocket.Server{
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
		Handler: func(conn *websocket.Conn) {
			conn.MaxPayloadBytes = 1 << 20
			h.serveWebSocket(r.Context(), conn, target)
		},
	}
	server.ServeHTTP(w, r)
}

func (h *httpGatewayHandler) serveWebSocket(ctx context.Context, conn *websocket.Conn, target *DaemonSession) {
	history, events, detach := target.Attach()
	defer detach()
	if !writeWS(conn, controlMessage{Type: "attached", Process: ptrSnapshot(target.Snapshot())}) {
		return
	}
	for i := range history {
		if !writeWS(conn, controlMessage{Type: "event", Event: &history[i]}) {
			return
		}
	}
	if !writeWS(conn, controlMessage{Type: "ready"}) {
		return
	}
	requests := make(chan controlMessage)
	done := make(chan struct{})
	defer close(done)
	go readWS(conn, requests, done)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok || !writeWS(conn, controlMessage{Type: "event", Event: &event}) {
				return
			}
		case request, ok := <-requests:
			if !ok || request.Type == "detach" {
				return
			}
			if request.Type == "input" {
				response := controlMessage{Type: "input_result", ID: request.ID}
				if err := target.Submit(request.Text); err != nil {
					response.Error = err.Error()
				}
				if !writeWS(conn, response) {
					return
				}
				continue
			}
			if request.Type == "stop" {
				response := controlMessage{Type: "stop_result", ID: request.ID}
				if err := target.Stop(); err != nil {
					response.Error = err.Error()
				}
				if !writeWS(conn, response) {
					return
				}
			}
		}
	}
}

func (h *httpGatewayHandler) find(id string) (*DaemonSession, error) {
	target := h.registry.find(id)
	if target == nil {
		return nil, errRuntimeNotFound
	}
	return target, nil
}

func readWS(conn *websocket.Conn, out chan<- controlMessage, done <-chan struct{}) {
	defer close(out)
	for {
		var data string
		if err := websocket.Message.Receive(conn, &data); err != nil {
			return
		}
		var msg controlMessage
		if json.Unmarshal([]byte(data), &msg) != nil {
			continue
		}
		select {
		case out <- msg:
		case <-done:
			return
		}
	}
}

func writeWS(conn *websocket.Conn, msg controlMessage) bool {
	data, err := json.Marshal(msg)
	if err != nil {
		return false
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err = websocket.Message.Send(conn, string(data))
	_ = conn.SetWriteDeadline(time.Time{})
	return err == nil
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, name string, payload any) bool {
	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func ptrSnapshot(snapshot ProcessSnapshot) *ProcessSnapshot { return &snapshot }

func statusForGatewayError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, errRuntimeNotFound) {
		return http.StatusNotFound
	}
	return http.StatusConflict
}

func (h *httpGatewayHandler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *httpGatewayHandler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, httpError{Error: message})
}
