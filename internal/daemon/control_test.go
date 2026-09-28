// 功能：验证 Unix Socket + NDJSON control transport 的错误路径和协议解析。
// 调用方：go test ./internal/daemon；不启动真实 Runtime/LLM。
// 全局状态：无；每个测试通过 net.Pipe 和空 Registry 构造隔离连接。
package daemon

import (
	"context"
	"encoding/json"
	"net"
	"testing"
)

func TestHandleControlConnAttachNotFound(t *testing.T) {
	registry := NewRegistry(context.Background())
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	go handleControlConn(context.Background(), serverConn, registry)

	enc := json.NewEncoder(clientConn)
	dec := json.NewDecoder(clientConn)
	if err := enc.Encode(controlMessage{Type: "attach", ProcessID: "missing"}); err != nil {
		t.Fatal(err)
	}
	if msg := readControlMsg(t, dec); msg.Type != "error" || msg.Error == "" {
		t.Fatalf("unexpected error message: %#v", msg)
	}
}

func TestShouldReplayEventSkipsTransientPicker(t *testing.T) {
	if shouldReplayEvent(ProcessEvent{Type: ProcessEventPicker}) {
		t.Fatal("picker replay should be skipped")
	}
	if !shouldReplayEvent(ProcessEvent{Type: ProcessEventSystem}) {
		t.Fatal("system events should replay")
	}
}

func TestSnapshotStateEventMatchesSnapshot(t *testing.T) {
	event := snapshotStateEvent(ProcessSnapshot{State: ProcessIdle, Turn: 2, PromptTokens: 3, TotalTokens: 5, ContextWindow: 8})
	if event.Type != ProcessEventState || event.Busy {
		t.Fatalf("idle snapshot state event = %#v", event)
	}
	if event.Turn != 2 || event.PromptTokens != 3 || event.TotalTokens != 5 || event.ContextWindow != 8 {
		t.Fatalf("snapshot metadata not copied: %#v", event)
	}
	if !snapshotStateEvent(ProcessSnapshot{State: ProcessRunning}).Busy {
		t.Fatal("running snapshot should produce busy state event")
	}
}

func TestHandleControlConnListEmpty(t *testing.T) {
	registry := NewRegistry(context.Background())
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	go handleControlConn(context.Background(), serverConn, registry)

	enc := json.NewEncoder(clientConn)
	dec := json.NewDecoder(clientConn)
	if err := enc.Encode(controlMessage{Type: "list"}); err != nil {
		t.Fatal(err)
	}
	msg := readControlMsg(t, dec)
	if msg.Type != "list" || len(msg.Processes) != 0 {
		t.Fatalf("unexpected list message: %#v", msg)
	}
}

func readControlMsg(t *testing.T, dec *json.Decoder) controlMessage {
	t.Helper()
	var msg controlMessage
	if err := dec.Decode(&msg); err != nil {
		t.Fatalf("decode control message: %v", err)
	}
	return msg
}
