// 功能：提供 daemon HTTP transport 的 token、Origin 和监听地址校验。
// 调用方：internal/daemon/http.go 启动 HTTP/WebSocket/SSE Gateway 时使用。
// 全局状态：无；token 持久化在调用方指定的 runDir/http_token 文件中。
package daemon

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const httpTokenFile = "http_token"

// LoadOrCreateHTTPToken 读取或创建本机 HTTP Gateway token。
// 参数：runDir 是 daemon 运行态目录，通常为 ~/.walle/run。
// 副作用：必要时创建 runDir 和 http_token，权限分别为 0700 与 0600。
func LoadOrCreateHTTPToken(runDir string) (string, error) {
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return "", fmt.Errorf("create daemon run dir: %w", err)
	}
	path := filepath.Join(runDir, httpTokenFile)
	if data, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(data)), nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read http token: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate http token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write http token: %w", err)
	}
	return token, nil
}

// CheckHTTPToken 校验 Authorization Bearer 或 query token。
func CheckHTTPToken(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	got := ""
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		got = strings.TrimSpace(auth[len("Bearer "):])
	} else {
		got = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// CheckOrigin 校验浏览器 Origin；非浏览器客户端通常没有 Origin，默认放行。
func CheckOrigin(r *http.Request, allowed []string, serverAddr string) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	for _, item := range allowed {
		if origin == strings.TrimSpace(item) {
			return true
		}
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return false
	}
	_, serverPort, err := net.SplitHostPort(serverAddr)
	if err != nil || serverPort == "" {
		return false
	}
	return u.Port() == serverPort
}

// ValidateLoopbackAddr 确认 HTTP Gateway 默认只监听本机地址。
func ValidateLoopbackAddr(addr string, allowNonLoopback bool) error {
	if allowNonLoopback {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("parse http addr %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("http addr %q is not loopback; pass --allow-non-loopback to allow it", addr)
}
