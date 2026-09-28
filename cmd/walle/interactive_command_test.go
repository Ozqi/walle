// 功能：验证交互 daemon 启动失败时输出可定位 session、日志和退出原因的诊断信息。
// 调用方：go test ./cmd/walle；测试只写隔离 HOME 下的 run/logs/session 文件。
// 全局状态：依赖包级 CLI flag 变量，测试中显式重置。
package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ozqi/walle/internal/daemon"
)

func TestDaemonStartErrorIncludesSessionDiagnostics(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessionID = "session-under-test"
	runDir := filepath.Join(home, ".walle", "run")
	if err := os.MkdirAll(filepath.Join(home, ".walle", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "interactive.log"), []byte("old line\nnew line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".walle", "sessions", "session-under-test.jsonl"), []byte(`{"type":"session","id":"session-under-test"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := daemonStartError(runDir, daemon.OpenRequest{Workspace: "/tmp/work", SessionID: sessionID, ModelRef: "demo/model", Debug: true}, "interactive daemon exited before ready", errors.New("signal: killed"))
	text := err.Error()
	for _, want := range []string{"workspace=/tmp/work", "session=session-under-test", "model=demo/model", "debug=true", "interactive.log", "signal: killed", "session-under-test.jsonl", "large histories can raise startup memory", "new line"} {
		if !strings.Contains(text, want) {
			t.Fatalf("daemonStartError missing %q:\n%s", want, text)
		}
	}
}
