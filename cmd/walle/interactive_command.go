package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Ozqi/walle/internal/daemon"
	"github.com/Ozqi/walle/internal/utils"
)

// startInteractiveClient 启动脱离当前终端的 daemon，并等待其 Unix Socket 可接入。
func startInteractiveClient(ctx context.Context) (*daemon.ProcessClient, error) {
	// 1. 解析可执行文件和运行目录，优先复用已就绪的交互 daemon。
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate executable: %w", err)
	}
	configDir, err := utils.GetConfigDir()
	if err != nil {
		return nil, err
	}
	runDir := filepath.Join(configDir, "run")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, fmt.Errorf("create daemon run dir: %w", err)
	}
	openReq, err := interactiveOpenRequest()
	if err != nil {
		return nil, err
	}
	if client, err := daemon.OpenProcess(runDir, openReq); err == nil {
		return client, nil
	} else if !daemon.IsSupervisorUnavailable(err) {
		return nil, err
	}

	// 2. daemon 不存在时脱离当前终端启动，并把 stdout/stderr 追加到固定日志。
	logFile, err := os.OpenFile(filepath.Join(runDir, "interactive.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open daemon log: %w", err)
	}
	args := interactiveDaemonArgs()
	process := exec.Command(executable, args...)
	process.Stdout, process.Stderr = logFile, logFile
	process.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := process.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("start interactive daemon: %w", err)
	}
	_ = logFile.Close()
	exited := make(chan error, 1)
	go func() {
		exited <- process.Wait()
	}()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	// 3. 轮询 open，直到 Socket 就绪并成功创建本次 workspace Runtime。
	for {
		client, err := daemon.OpenProcess(runDir, openReq)
		if err == nil {
			return client, nil
		}
		if !daemon.IsSupervisorUnavailable(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-exited:
			return nil, daemonStartError(runDir, openReq, fmt.Sprintf("interactive daemon exited before ready: %v", err), err)
		case <-deadline.C:
			return nil, daemonStartError(runDir, openReq, "interactive daemon did not become ready", nil)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func daemonStartError(runDir string, req daemon.OpenRequest, summary string, exitErr error) error {
	logPath := filepath.Join(runDir, "interactive.log")
	diagnostics := []string{
		fmt.Sprintf("diagnostics: workspace=%s session=%s model=%s debug=%v", fallback(req.Workspace, "-"), fallback(req.SessionID, "-"), fallback(req.ModelRef, "-"), req.Debug),
		fmt.Sprintf("log: %s", logPath),
	}
	if exitErr != nil {
		diagnostics = append(diagnostics, fmt.Sprintf("exit: %s", exitErr))
		if status, ok := exitErr.(*exec.ExitError); ok {
			diagnostics = append(diagnostics, fmt.Sprintf("signal: %s", status.ProcessState.String()))
		}
	}
	if req.SessionID != "" {
		diagnostics = append(diagnostics, sessionDiagnostic(req.SessionID))
	}
	if data, err := os.ReadFile(logPath); err == nil {
		if text := strings.TrimSpace(string(data)); text != "" {
			lines := strings.Split(text, "\n")
			if len(lines) > 8 {
				lines = lines[len(lines)-8:]
			}
			diagnostics = append(diagnostics, "last log lines:", strings.Join(lines, "\n"))
		}
	}
	return fmt.Errorf("%s\n%s", summary, strings.Join(diagnostics, "\n"))
}

func sessionDiagnostic(id string) string {
	configDir, err := utils.GetConfigDir()
	if err != nil {
		return "session: unable to resolve config directory: " + err.Error()
	}
	path := filepath.Join(configDir, "sessions", id+".jsonl")
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("session: %s (%v)", path, err)
	}
	return fmt.Sprintf("session: %s (%d bytes; large histories can raise startup memory)", path, info.Size())
}

func fallback(value string, fallbackValue string) string {
	if strings.TrimSpace(value) == "" {
		return fallbackValue
	}
	return value
}

func interactiveOpenRequest() (daemon.OpenRequest, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return daemon.OpenRequest{}, fmt.Errorf("get workspace: %w", err)
	}
	return daemon.OpenRequest{
		Workspace: cwd, Continue: continueLast, SessionID: sessionID,
		ModelRef: modelRef, LLMFormat: llmFormat, LLMModel: llmModel,
		Debug: debugMode, PromptBase: "tui",
	}, nil
}

func interactiveDaemonArgs() []string {
	args := make([]string, 0, 2)
	if debugMode {
		args = append(args, "--debug")
	}
	return append(args, "daemon")
}
