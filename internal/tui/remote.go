package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/Ozqi/walle/internal/daemon"
	tea "github.com/charmbracelet/bubbletea"
)

// RemoteClient 是任意 attached TUI 所需的最小 daemon 客户端契约。
type RemoteClient interface {
	Snapshot() daemon.ProcessSnapshot
	Events() <-chan daemon.ProcessEvent
	Submit(string) error
	Stop() error
	Close() error
}

// LaunchAttachedTUI 连接 daemon Agent；退出界面只关闭客户端连接。
func LaunchAttachedTUI(ctx context.Context, client RemoteClient) error {
	// 1. 用 attach 快照初始化界面，并把输入和停止操作绑定到远端客户端。
	snapshot := client.Snapshot()
	model := NewAppModel(ctx, snapshot.Model, snapshot.SessionID)
	model.remoteTurn = snapshot.Turn
	model.promptTokens = snapshot.PromptTokens
	model.totalTokens = snapshot.TotalTokens
	model.contextWindow = snapshot.ContextWindow
	model.remoteSubmit = client.Submit
	model.remoteStop = client.Stop
	model.busy = snapshot.State == daemon.ProcessRunning
	if model.busy {
		model.currentStatus = "attached"
	}
	model.metaCache = cachedMeta{Workdir: snapshot.Workspace, LoadedAt: time.Now()}
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	model.program = p
	// 2. Events 已合并历史重放和实时流；流关闭时通知界面远端已断开。
	go func() {
		for event := range client.Events() {
			p.Send(remoteEventMsg{event: event})
		}
		p.Send(remoteDisconnectedMsg{})
	}()
	_, err := p.Run()
	// 3. TUI 退出只发送 detach/Close，远端执行继续存活，可再次 attach。
	_ = client.Close()
	if err == nil && snapshot.ID != "" {
		fmt.Printf("Detached from %s; agent is still running.\n", snapshot.ID)
		fmt.Printf("Check: walle ps\nReattach: walle attach %s\n", snapshot.ID)
	}
	return err
}
