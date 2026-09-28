package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Ozqi/walle/internal/daemon"
	"github.com/Ozqi/walle/internal/toolevent"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestSystemEventClearsAssistantWaiting(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	model.showAssistantWaiting()

	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventSystem, Text: "stopped current run"}})

	for _, entry := range model.entries {
		if entry.Role == roleAssistant && entry.Content == "" {
			t.Fatal("empty assistant waiting entry should be removed after system event")
		}
	}
	if model.busy {
		t.Fatal("system event should stop current run")
	}
}

func TestAssistantDoneReportsEmptyResponse(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	model.showAssistantWaiting()

	_, _ = model.Update(assistantDoneMsg{})

	for _, entry := range model.entries {
		if entry.Role == roleSystem && entry.Content == "assistant returned empty response" {
			return
		}
	}
	t.Fatal("empty assistant response should be reported as a system entry")
}

func TestAssistantDoneSkipsEmptyResponseWarningAfterToken(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	_, _ = model.Update(assistantTokenMsg{token: "ok"})
	_, _ = model.Update(assistantDoneMsg{})

	for _, entry := range model.entries {
		if entry.Role == roleSystem && entry.Content == "assistant returned empty response" {
			t.Fatal("non-empty assistant response should not report empty warning")
		}
	}
	if model.busy || model.currentStatus != "done" {
		t.Fatalf("completed run state = busy:%v status:%q, want idle done", model.busy, model.currentStatus)
	}
	footer := stripANSI(renderInputFooter(model.snapshot(), model.modelName, 100))
	if !strings.Contains(footer, "done") {
		t.Fatalf("completed run footer should keep terminal state visible: %q", footer)
	}
}

func TestInputHistoryBrowsesSubmittedInputs(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.remoteSubmit = func(string) error { return nil }

	model.input.SetValue("first")
	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model.busy = false
	model.input.SetValue("second")
	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	model.input.SetValue("draft")
	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := model.input.Value(); got != "second" {
		t.Fatalf("first up = %q, want second", got)
	}
	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := model.input.Value(); got != "first" {
		t.Fatalf("second up = %q, want first", got)
	}
	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := model.input.Value(); got != "second" {
		t.Fatalf("first down = %q, want second", got)
	}
	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := model.input.Value(); got != "draft" {
		t.Fatalf("second down = %q, want draft", got)
	}
}

func TestBusyQueueAndInputClearKeepsStateCoherent(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.remoteSubmit = func(string) error { return nil }
	model.startRun("running")
	model.input.SetValue("queued message")

	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if model.pendingInput != "queued message" || model.input.Value() != "" {
		t.Fatalf("busy enter should queue and clear input, pending=%q input=%q", model.pendingInput, model.input.Value())
	}

	model.input.SetValue("draft")
	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if model.input.Value() != "" || model.pendingInput != "queued message" || !model.busy || model.currentStatus != "running" {
		t.Fatalf("ctrl-c should clear draft without stopping run, hiding running status, or dropping queue, busy=%v status=%q pending=%q input=%q", model.busy, model.currentStatus, model.pendingInput, model.input.Value())
	}
}

func TestQueuedUserEchoIsNotDuplicatedByRemoteReplay(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.remoteSubmit = func(string) error { return nil }
	model.startRun("running")
	model.input.SetValue("queued message")

	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventUser, Text: "queued message"}})

	count := 0
	for _, entry := range model.entries {
		if entry.Role == roleUser && entry.Content == "queued message" {
			count++
		}
	}
	if count != 1 || len(model.userEchoes) != 0 {
		t.Fatalf("queued user echo replay count=%d remainingEchoes=%d, want one entry and no pending echo", count, len(model.userEchoes))
	}
}

func TestDoubleCtrlCQuitsAfterClearingDraft(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.pendingInput = "queued message"
	model.input.SetValue("draft")

	_, first := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if first != nil || model.input.Value() != "" || model.pendingInput != "queued message" {
		t.Fatalf("first ctrl-c should clear draft only, cmd:%v input:%q pending:%q", first != nil, model.input.Value(), model.pendingInput)
	}
	_, second := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if second == nil || model.pendingInput != "queued message" {
		t.Fatalf("second ctrl-c should quit without dropping queued input, cmd:%v pending:%q", second != nil, model.pendingInput)
	}
}

func TestCtrlDDetachWhileBusyKeepsPendingQueue(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	model.pendingInput = "queued message"
	model.input.SetValue("draft")

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd == nil || !model.busy || model.pendingInput != "queued message" || model.input.Value() != "draft" {
		t.Fatalf("ctrl-d should detach without mutating active run state, cmd:%v busy:%v pending:%q input:%q", cmd != nil, model.busy, model.pendingInput, model.input.Value())
	}
}

func TestDoubleCtrlCWhileBusyQuitsWithoutDroppingQueue(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	model.pendingInput = "queued message"
	model.input.SetValue("draft")

	_, first := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if first != nil || !model.busy || model.currentStatus != "running" || model.pendingInput != "queued message" || model.input.Value() != "" {
		t.Fatalf("first busy ctrl-c should clear draft and keep run/queue, cmd:%v busy:%v status:%q pending:%q input:%q", first != nil, model.busy, model.currentStatus, model.pendingInput, model.input.Value())
	}
	_, second := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if second == nil || !model.busy || model.pendingInput != "queued message" {
		t.Fatalf("second busy ctrl-c should quit without dropping run state or queue, cmd:%v busy:%v pending:%q", second != nil, model.busy, model.pendingInput)
	}
}

func TestFooterFitsNarrowWidthWithLongMetadata(t *testing.T) {
	model := NewAppModel(context.Background(), "provider/"+strings.Repeat("very-long-model-name-", 4), "test-session")
	model.metaCache = cachedMeta{Workdir: "/tmp/" + strings.Repeat("very-long-workdir/", 4)}
	model.promptTokens = 123456
	model.totalTokens = 789012
	model.contextWindow = 1000000
	model.pendingInput = "queued message"
	model.currentStatus = "disconnected"

	rendered := stripANSI(renderInputFooter(model.snapshot(), model.modelName, 40))
	for _, line := range strings.Split(rendered, "\n") {
		if width := lipgloss.Width(line); width > 40 {
			t.Fatalf("footer line width = %d, want <= 40:\n%s", width, rendered)
		}
	}
}

func TestRemoteDoneThenIdleStateKeepsDoneVisible(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventAssistant, Text: "ok"}})
	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventDone}})
	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventState, Busy: false}})

	if model.busy || model.currentStatus != "done" {
		t.Fatalf("remote done followed by idle state = busy:%v status:%q, want idle done", model.busy, model.currentStatus)
	}
	footer := stripANSI(renderInputFooter(model.snapshot(), model.modelName, 100))
	if !strings.Contains(footer, "done") {
		t.Fatalf("remote footer should keep done visible after idle state: %q", footer)
	}
}

func TestRemoteErrorThenIdleStateKeepsErrorVisible(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventError, Error: "LLM error: boom"}})
	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventState, Busy: false}})

	if model.busy || model.currentStatus != "error" {
		t.Fatalf("remote error followed by idle state = busy:%v status:%q, want idle error", model.busy, model.currentStatus)
	}
	footer := stripANSI(renderInputFooter(model.snapshot(), model.modelName, 100))
	if !strings.Contains(footer, "error") {
		t.Fatalf("remote footer should keep error visible after idle state: %q", footer)
	}
}

func TestRemoteDisconnectIsDedupedAndBlocksSubmit(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	model.showAssistantWaiting()
	_, _ = model.Update(remoteDisconnectedMsg{})
	_, _ = model.Update(remoteDisconnectedMsg{})

	count := 0
	for _, entry := range model.entries {
		if entry.Role == roleSystem && entry.Content == "daemon disconnected" {
			count++
		}
		if entry.Role == roleAssistant && entry.Content == "" {
			t.Fatal("disconnect should clear empty assistant waiting entry")
		}
	}
	if count != 1 || model.busy || model.currentStatus != "disconnected" {
		t.Fatalf("disconnect state = count:%d busy:%v status:%q, want one disconnected entry and idle disconnected", count, model.busy, model.currentStatus)
	}
	if err := model.remoteSubmit("after disconnect"); err == nil || !strings.Contains(err.Error(), "daemon disconnected") {
		t.Fatalf("remoteSubmit after disconnect err = %v, want daemon disconnected", err)
	}
	model.input.SetValue("after disconnect")
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || model.currentStatus != "disconnected" || model.input.Value() != "after disconnect" || model.lastInput == "after disconnect" {
		t.Fatalf("submit after disconnect should be blocked locally without clearing input, cmd:%v status:%q input:%q last:%q", cmd != nil, model.currentStatus, model.input.Value(), model.lastInput)
	}
	model.input.SetValue("/stop")
	_, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || model.currentStatus != "disconnected" || model.input.Value() != "/stop" || model.lastInput == "/stop" {
		t.Fatalf("stop after disconnect should be blocked locally without clearing input, cmd:%v status:%q input:%q last:%q", cmd != nil, model.currentStatus, model.input.Value(), model.lastInput)
	}
	_, cmd = model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil || model.currentStatus != "disconnected" || model.input.Value() != "" {
		t.Fatalf("ctrl-c after disconnect should clear draft but keep disconnected status, cmd:%v status:%q input:%q", cmd != nil, model.currentStatus, model.input.Value())
	}
	model.input.SetValue("/detach")
	_, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || model.currentStatus != "disconnected" {
		t.Fatalf("detach after disconnect should still quit locally, cmd:%v status:%q", cmd != nil, model.currentStatus)
	}
}

func TestRemoteDisconnectThenIdleStateKeepsDisconnectedVisible(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")

	_, _ = model.Update(remoteDisconnectedMsg{})
	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventState, Busy: false}})
	if model.busy || model.currentStatus != "disconnected" {
		t.Fatalf("disconnect followed by idle state = busy:%v status:%q, want idle disconnected", model.busy, model.currentStatus)
	}
	footer := stripANSI(renderInputFooter(model.snapshot(), model.modelName, 100))
	if !strings.Contains(footer, "disconnected") {
		t.Fatalf("remote footer should keep disconnected visible after idle state: %q", footer)
	}
}

func TestRemoteDisconnectKeepsPendingQueueVisible(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	model.pendingInput = "queued follow-up"
	model.spinnerPending = true
	model.renderPending = true
	model.remoteTurn = 7
	model.promptTokens = 10
	model.totalTokens = 20
	model.contextWindow = 100
	model.toolCalls = 3
	model.lastTool = "read_file"
	model.userEchoes = []string{"queued follow-up"}
	model.currentAssistant = 99
	model.escPending = true
	model.quitPending = true

	_, _ = model.Update(remoteDisconnectedMsg{})
	if model.pendingInput != "queued follow-up" || model.currentStatus != "disconnected" || model.spinnerPending || model.renderPending || model.remoteTurn != 0 || model.totalTokens != 0 || model.toolCalls != 0 || model.lastTool != "" || len(model.userEchoes) != 0 || model.currentAssistant != -1 || model.escPending || model.quitPending || model.queueIntroBlink() != nil {
		t.Fatalf("disconnect should preserve queue and stop transient UI state, pending=%q status=%q spinner=%v render=%v turn=%d tokens=%d tools=%d lastTool=%q echoes=%d assistant=%d esc=%v quit=%v", model.pendingInput, model.currentStatus, model.spinnerPending, model.renderPending, model.remoteTurn, model.totalTokens, model.toolCalls, model.lastTool, len(model.userEchoes), model.currentAssistant, model.escPending, model.quitPending)
	}
	footer := stripANSI(renderInputFooter(model.snapshot(), model.modelName, 100))
	if !strings.Contains(footer, "disconnected") || !strings.Contains(footer, "queued") {
		t.Fatalf("disconnect footer should show both disconnected and queued: %q", footer)
	}
	if cmd := model.submitPendingInputCmd(); cmd != nil || model.pendingInput != "queued follow-up" {
		t.Fatalf("disconnect should block pending auto-submit, cmd:%v pending:%q", cmd != nil, model.pendingInput)
	}
}

func TestRemoteEventAfterDisconnectIsIgnored(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	_, _ = model.Update(remoteDisconnectedMsg{})

	before := len(model.entries)
	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventState, Busy: true}})
	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventAssistant, Text: "late token"}})
	if model.busy || model.currentStatus != "disconnected" || len(model.entries) != before {
		t.Fatalf("late remote events after disconnect should be ignored, busy=%v status=%q entries=%#v", model.busy, model.currentStatus, model.entries)
	}
}

func TestRemoteSubmitResultAfterDisconnectIsIgnored(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	_, _ = model.Update(remoteDisconnectedMsg{})

	before := len(model.entries)
	_, _ = model.Update(remoteSubmitResultMsg{text: "/provider demo", err: errSubmitForTest{}})
	if model.currentStatus != "disconnected" || len(model.entries) != before {
		t.Fatalf("late submit result after disconnect should be ignored, status=%q entries=%#v", model.currentStatus, model.entries)
	}
}

func TestRemoteStopResultAfterDisconnectIsIgnored(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	_, _ = model.Update(remoteDisconnectedMsg{})

	before := len(model.entries)
	_, _ = model.Update(remoteStopResultMsg{err: errStopForTest{}})
	if model.currentStatus != "disconnected" || len(model.entries) != before {
		t.Fatalf("late stop result after disconnect should be ignored, status=%q entries=%#v", model.currentStatus, model.entries)
	}
}

func TestLocalAssistantTerminalAfterDisconnectIsIgnored(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	_, _ = model.Update(remoteDisconnectedMsg{})

	before := len(model.entries)
	_, _ = model.Update(assistantDoneMsg{})
	_, _ = model.Update(assistantErrorMsg{err: errTestModelFailure{}})
	if model.currentStatus != "disconnected" || len(model.entries) != before {
		t.Fatalf("late local terminal messages after disconnect should be ignored, status=%q entries=%#v", model.currentStatus, model.entries)
	}
}

func TestAssistantTokenAfterDisconnectIsIgnored(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	_, _ = model.Update(remoteDisconnectedMsg{})

	before := len(model.entries)
	_, cmd := model.Update(assistantTokenMsg{token: "late token"})
	if cmd != nil || model.currentStatus != "disconnected" || len(model.entries) != before {
		t.Fatalf("late assistant token after disconnect should be ignored, cmd:%v status=%q entries=%#v", cmd != nil, model.currentStatus, model.entries)
	}
}

func TestAssistantThinkingAfterDisconnectIsIgnored(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	_, _ = model.Update(remoteDisconnectedMsg{})

	before := len(model.entries)
	_, cmd := model.Update(assistantThinkingMsg{token: "late thinking"})
	if cmd != nil || model.currentStatus != "disconnected" || len(model.entries) != before {
		t.Fatalf("late thinking after disconnect should be ignored, cmd:%v status=%q entries=%#v", cmd != nil, model.currentStatus, model.entries)
	}
}

func TestRenderTickAfterDisconnectDoesNotRestartIntroBlink(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.introFocus = true
	_, _ = model.Update(remoteDisconnectedMsg{})

	_, cmd := model.Update(renderTickMsg{})
	if cmd != nil || model.currentStatus != "disconnected" || model.renderPending {
		t.Fatalf("render tick after disconnect should not schedule more animation, cmd:%v status=%q renderPending:%v", cmd != nil, model.currentStatus, model.renderPending)
	}
}

func TestSpinnerTickAfterDisconnectDoesNotAnimate(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	model.spinnerPending = true
	_, _ = model.Update(remoteDisconnectedMsg{})

	_, cmd := model.Update(spinnerTickMsg{})
	if cmd != nil || model.currentStatus != "disconnected" || model.spinnerFrame != 0 || model.spinnerPending {
		t.Fatalf("spinner tick after disconnect should not animate, cmd:%v status=%q frame=%d pending=%v", cmd != nil, model.currentStatus, model.spinnerFrame, model.spinnerPending)
	}
}

func TestToolEventAfterDisconnectIsIgnored(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	_, _ = model.Update(remoteDisconnectedMsg{})

	before := len(model.entries)
	_, _ = model.Update(toolEventMsg{event: testToolEvent("call")})
	if model.currentStatus != "disconnected" || len(model.entries) != before || model.toolCalls != 0 {
		t.Fatalf("late tool event after disconnect should be ignored, status=%q toolCalls=%d entries=%#v", model.currentStatus, model.toolCalls, model.entries)
	}
}

func TestRemoteDisconnectClosesPicker(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.picker = &pickerState{Kind: "provider", Options: []string{"demo"}}

	_, _ = model.Update(remoteDisconnectedMsg{})
	if model.picker != nil || model.currentStatus != "disconnected" {
		t.Fatalf("disconnect should close transient picker, picker:%v status:%q", model.picker != nil, model.currentStatus)
	}
}

func TestPickerSubmitStaysCommandNotBusy(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	submitted := ""
	model.remoteSubmit = func(text string) error {
		submitted = text
		return nil
	}
	model.picker = &pickerState{Kind: "model", Provider: "demo", Options: []string{"fast"}}

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || model.picker != nil || model.busy || model.currentStatus != "command" {
		t.Fatalf("picker enter state = cmd:%v picker:%v busy:%v status:%q", cmd != nil, model.picker != nil, model.busy, model.currentStatus)
	}
	msg, ok := cmd().(remoteSubmitResultMsg)
	if !ok || msg.text != "/model demo/fast" || submitted != "/model demo/fast" {
		t.Fatalf("picker command = msg:%#v submitted:%q", msg, submitted)
	}
	_, _ = model.Update(msg)
	if model.busy || model.currentStatus != "idle" {
		t.Fatalf("picker command completion = busy:%v status:%q, want idle", model.busy, model.currentStatus)
	}
}

func TestModelEventWhileBusyDoesNotStopRun(t *testing.T) {
	model := NewAppModel(context.Background(), "old-model", "test-session")
	model.startRun("running")

	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventModel, Text: "new-model"}})
	if !model.busy || model.currentStatus != "running" || model.modelName != "new-model" {
		t.Fatalf("model event during run = busy:%v status:%q model:%q", model.busy, model.currentStatus, model.modelName)
	}
}

func TestPickerEventWhileBusyKeepsRunActive(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")

	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventPicker, Kind: "provider", Options: []string{"demo"}}})
	if !model.busy || model.currentStatus != "select provider" || model.picker == nil {
		t.Fatalf("picker event during run = busy:%v status:%q picker:%v", model.busy, model.currentStatus, model.picker != nil)
	}
}

func TestPickerEscWhileBusyKeepsRunActive(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	model.picker = &pickerState{Kind: "provider", Options: []string{"demo"}}

	_, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !model.busy || model.currentStatus != "running" || model.picker != nil {
		t.Fatalf("picker esc during run = busy:%v status:%q picker:%v", model.busy, model.currentStatus, model.picker != nil)
	}
}

func TestPickerCtrlCWhileBusyClosesPickerOnly(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")
	model.picker = &pickerState{Kind: "provider", Options: []string{"demo"}}

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil || !model.busy || model.currentStatus != "running" || model.picker != nil || model.quitPending {
		t.Fatalf("picker ctrl-c during run = cmd:%v busy:%v status:%q picker:%v quitPending:%v", cmd != nil, model.busy, model.currentStatus, model.picker != nil, model.quitPending)
	}
}

func TestDoneAutoSubmitsQueuedInput(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	submitted := ""
	model.remoteSubmit = func(text string) error {
		submitted = text
		return nil
	}
	model.startRun("running")
	model.pendingInput = "queued follow-up"

	_, cmd := model.Update(assistantDoneMsg{})
	if cmd == nil || model.pendingInput != "" || !model.busy || model.currentStatus != "submitting queued" {
		t.Fatalf("done should start queued submit, cmd:%v busy:%v status:%q pending:%q", cmd != nil, model.busy, model.currentStatus, model.pendingInput)
	}
	if !runCmdTreeHasSubmit(cmd, "queued follow-up") || submitted != "queued follow-up" {
		t.Fatalf("done command should submit queued input, submitted=%q", submitted)
	}
}

func TestQueuedSubmitBusyErrorStaysQueued(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.pendingInput = "queued follow-up"
	model.startRun("submitting queued")

	_, _ = model.Update(remoteSubmitResultMsg{text: "queued follow-up", err: errAgentBusyForTest{}})
	if !model.busy || model.pendingInput != "queued follow-up" || model.currentStatus != "queued" {
		t.Fatalf("busy submit error should preserve queue, busy=%v pending=%q status=%q", model.busy, model.pendingInput, model.currentStatus)
	}
}

func TestQueuedSubmitErrorWhileBusyKeepsQueue(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.pendingInput = "queued follow-up"
	model.startRun("submitting queued")

	_, _ = model.Update(remoteSubmitResultMsg{text: "queued follow-up", err: errSubmitForTest{}})
	if !model.busy || model.pendingInput != "queued follow-up" || model.currentStatus != "queued" {
		t.Fatalf("queued submit error should keep current run and queue, busy=%v pending=%q status=%q", model.busy, model.pendingInput, model.currentStatus)
	}
}

func TestQueuedSubmitAckWhileBusyDoesNotAddWaitingBullet(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.pendingInput = "queued follow-up"
	model.startRun("submitting queued")
	model.showAssistantWaiting()
	before := len(model.entries)

	_, _ = model.Update(remoteSubmitResultMsg{text: "queued follow-up"})
	if !model.busy || model.pendingInput != "" || len(model.entries) != before {
		t.Fatalf("busy submit ack should only clear queue, busy=%v pending=%q entries=%d before=%d", model.busy, model.pendingInput, len(model.entries), before)
	}
}

func TestErrorAutoSubmitsQueuedInput(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	submitted := ""
	model.remoteSubmit = func(text string) error {
		submitted = text
		return nil
	}
	model.startRun("running")
	model.pendingInput = "queued after error"

	_, cmd := model.Update(assistantErrorMsg{err: errTestModelFailure{}})
	if cmd == nil || model.pendingInput != "" || !model.busy || model.currentStatus != "submitting queued" {
		t.Fatalf("error should terminate current run and submit queue, cmd:%v busy:%v status:%q pending:%q", cmd != nil, model.busy, model.currentStatus, model.pendingInput)
	}
	if !runCmdTreeHasSubmit(cmd, "queued after error") || submitted != "queued after error" {
		t.Fatalf("error command should submit queued input, submitted=%q", submitted)
	}
}

func TestStopSystemEventAutoSubmitsQueuedInput(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	submitted := ""
	model.remoteSubmit = func(text string) error {
		submitted = text
		return nil
	}
	model.startRun("running")
	model.pendingInput = "queued after stop"
	model.showAssistantWaiting()

	_, cmd := model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventSystem, Text: "stopped current run"}})
	if cmd == nil || model.pendingInput != "" || !model.busy || model.currentStatus != "submitting queued" {
		t.Fatalf("stop system event should submit queue, cmd:%v busy:%v status:%q pending:%q", cmd != nil, model.busy, model.currentStatus, model.pendingInput)
	}
	if !runCmdTreeHasSubmit(cmd, "queued after stop") || submitted != "queued after stop" {
		t.Fatalf("stop command should submit queued input, submitted=%q", submitted)
	}
}

func TestRemoteStopErrorClearsWaitingAndMarksError(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("stopping")
	model.showAssistantWaiting()

	_, _ = model.Update(remoteStopResultMsg{err: errStopForTest{}})
	if model.busy || model.currentStatus != "error" {
		t.Fatalf("stop error should end run as error, busy=%v status=%q", model.busy, model.currentStatus)
	}
	for _, entry := range model.entries {
		if entry.Role == roleAssistant && entry.Content == "" {
			t.Fatal("stop error should clear empty assistant waiting entry")
		}
	}
}

func testToolEvent(kind string) toolevent.ToolEvent {
	return toolevent.ToolEvent{Kind: kind, Name: "base.read_file", Args: `{"path":"README.md"}`}
}

type errStopForTest struct{}

func (errStopForTest) Error() string { return "stop failed" }

type errTestModelFailure struct{}

func (errTestModelFailure) Error() string { return "model failed" }

func TestSlashBusyErrorDoesNotStopCurrentRun(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")

	_, _ = model.Update(remoteSubmitResultMsg{text: "/model demo/fast", err: errAgentBusyForTest{}})
	if !model.busy || model.currentStatus != "running" {
		t.Fatalf("slash busy error should keep current run alive, busy=%v status=%q", model.busy, model.currentStatus)
	}
	for _, entry := range model.entries {
		if entry.Role == roleSystem && strings.Contains(entry.Content, "agent is busy") {
			t.Fatalf("transient busy slash error should not append noisy system entry: %#v", entry)
		}
	}
}

func TestSlashBusySystemEventDoesNotStopCurrentRun(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")

	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventSystem, Text: "agent is busy"}})
	if !model.busy || model.currentStatus != "running" {
		t.Fatalf("busy system event should keep current run alive, busy=%v status=%q", model.busy, model.currentStatus)
	}
	for _, entry := range model.entries {
		if entry.Role == roleSystem && strings.Contains(entry.Content, "agent is busy") {
			t.Fatalf("transient busy system event should not append noisy system entry: %#v", entry)
		}
	}
}

func TestSystemEventWhileBusyDoesNotStopCurrentRun(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")

	_, _ = model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventSystem, Text: "provider warning"}})
	if !model.busy || model.currentStatus != "running" {
		t.Fatalf("system event during run should keep current run alive, busy=%v status=%q", model.busy, model.currentStatus)
	}
}

func TestSystemEventWhileBusyKeepsQueueAndWaiting(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("submitting")
	model.pendingInput = "queued follow-up"
	model.showAssistantWaiting()
	before := len(model.entries)

	_, cmd := model.Update(remoteEventMsg{event: daemon.ProcessEvent{Type: daemon.ProcessEventSystem, Text: "provider warning"}})
	if cmd != nil || !model.busy || model.pendingInput != "queued follow-up" || model.currentStatus != "submitting" {
		t.Fatalf("system event during run should not submit queue, stop run, or overwrite phase, cmd:%v busy:%v pending:%q status:%q", cmd != nil, model.busy, model.pendingInput, model.currentStatus)
	}
	if len(model.entries) != before+1 || model.entries[before-1].Role != roleAssistant || model.entries[before-1].Content != "" {
		t.Fatalf("system event during run should preserve waiting entry and append one system row: %#v", model.entries)
	}
}

func TestSlashSubmitAckWhileBusyDoesNotMarkIdle(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")

	_, _ = model.Update(remoteSubmitResultMsg{text: "/provider demo"})
	if !model.busy || model.currentStatus != "running" {
		t.Fatalf("slash submit ack during run should keep running state, busy=%v status=%q", model.busy, model.currentStatus)
	}
}

func TestSubmitErrorWhileBusyDoesNotStopCurrentRun(t *testing.T) {
	model := NewAppModel(context.Background(), "test-model", "test-session")
	model.startRun("running")

	_, _ = model.Update(remoteSubmitResultMsg{text: "/provider demo", err: errSubmitForTest{}})
	if !model.busy || model.currentStatus != "running" {
		t.Fatalf("submit error during run should keep current run alive, busy=%v status=%q", model.busy, model.currentStatus)
	}
}

type errSubmitForTest struct{}

func (errSubmitForTest) Error() string { return "submit failed" }

type errAgentBusyForTest struct{}

func (errAgentBusyForTest) Error() string { return "agent is busy" }

func runCmdTreeHasSubmit(cmd tea.Cmd, want string) bool {
	msg := cmd()
	if result, ok := msg.(remoteSubmitResultMsg); ok {
		return result.text == want
	}
	if cmds, ok := msg.(tea.BatchMsg); ok {
		for _, child := range cmds {
			if runCmdTreeHasSubmit(child, want) {
				return true
			}
		}
	}
	return false
}
