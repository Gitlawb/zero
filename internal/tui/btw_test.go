package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/sessions"
)

func newBTWTestModel(t *testing.T) model {
	t.Helper()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	parent, err := store.Create(sessions.CreateInput{
		SessionID: "main-session",
		Title:     "Main task",
		Cwd:       "/repo",
		ModelID:   "test-model",
		Provider:  "test-provider",
	})
	if err != nil {
		t.Fatalf("create parent session: %v", err)
	}
	for _, payload := range []map[string]any{
		{"role": "user", "content": "implement the main task"},
		{"role": "assistant", "content": "working on it"},
	} {
		if _, err := store.AppendEvent(parent.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: payload}); err != nil {
			t.Fatalf("append parent event: %v", err)
		}
	}
	loaded, err := store.Get(parent.SessionID)
	if err != nil || loaded == nil {
		t.Fatalf("load parent session: session=%#v err=%v", loaded, err)
	}
	events, err := store.ReadEvents(parent.SessionID)
	if err != nil {
		t.Fatalf("read parent events: %v", err)
	}
	m := newModel(context.Background(), Options{
		Cwd:          "/repo",
		ModelName:    "test-model",
		ProviderName: "test-provider",
		SessionStore: store,
	})
	m.activeSession = *loaded
	m.sessionEvents = events
	m.transcript = appendTranscriptRowsDedup(initialTranscript(), transcriptRowsFromSessionEvents(events))
	return m
}

func TestBTWCommandParsesInlineQuestion(t *testing.T) {
	got := parseCommand("/btw double-check the approach")
	if got.kind != commandBTW || got.text != "double-check the approach" {
		t.Fatalf("parseCommand(/btw ...) = %#v", got)
	}
}

func TestBTWCreatesIsolatedForkAndReturnsWithoutMerge(t *testing.T) {
	m := newBTWTestModel(t)
	parentID := m.activeSession.SessionID
	parentEventCount := len(m.sessionEvents)

	side, cmd := m.handleBTWCommand("")
	if cmd != nil {
		t.Fatal("bare /btw should not start an agent run")
	}
	if !side.btw.active || side.btw.parent == nil {
		t.Fatal("expected active BTW state with saved parent")
	}
	if side.activeSession.SessionKind != sessions.SessionKindSide || side.activeSession.ParentSessionID != parentID {
		t.Fatalf("side metadata = %#v", side.activeSession)
	}
	if side.activeSession.Tag != btwSessionTag {
		t.Fatalf("side tag = %q, want %q", side.activeSession.Tag, btwSessionTag)
	}
	if len(side.sessionEvents) <= parentEventCount {
		t.Fatalf("side should contain copied context plus fork marker: got %d events", len(side.sessionEvents))
	}
	if !strings.Contains(side.sessionPrompt("question"), btwContextBoundary) {
		t.Fatal("side prompt is missing the inherited-context boundary")
	}

	updated, err := side.appendSessionEvent(sessions.EventMessage, map[string]any{
		"role": "assistant", "content": "side-only answer",
	})
	if err != nil {
		t.Fatalf("append side event: %v", err)
	}
	returned, _ := updated.leaveBTW()
	if returned.activeSession.SessionID != parentID {
		t.Fatalf("returned session = %q, want %q", returned.activeSession.SessionID, parentID)
	}
	if len(returned.sessionEvents) != parentEventCount {
		t.Fatalf("side events merged into parent: got %d events, want %d", len(returned.sessionEvents), parentEventCount)
	}
	parentEvents, err := returned.sessionStore.ReadEvents(parentID)
	if err != nil {
		t.Fatalf("read parent after return: %v", err)
	}
	for _, event := range parentEvents {
		if strings.Contains(string(event.Payload), "side-only answer") {
			t.Fatal("side-only event was persisted into the parent session")
		}
	}
}

func TestBTWCanOpenWhileParentRunContinues(t *testing.T) {
	m := newBTWTestModel(t)
	m.pending = true
	m.runID = 7
	m.activeRunID = 7

	side, _ := m.handleBTWCommand("")
	if side.pending || side.activeRunID != 0 {
		t.Fatalf("side inherited parent run state: pending=%v activeRunID=%d", side.pending, side.activeRunID)
	}
	if side.btw.parent == nil || !side.btw.parent.pending || side.btw.parent.activeRunID != 7 {
		t.Fatalf("parent run was not preserved: %#v", side.btw.parent)
	}

	routed, _, ok := side.routeBTWParentMessage(agentTextMsg{runID: 7, delta: "main progress"})
	if !ok {
		t.Fatal("parent run message was not routed")
	}
	if strings.Contains(routed.streamingTextString(), "main progress") {
		t.Fatal("parent streaming output leaked into the side transcript")
	}
	if routed.btw.parent == nil || !strings.Contains(routed.btw.parent.streamingTextString(), "main progress") {
		t.Fatal("parent streaming output was not retained on the hidden parent")
	}
}

func TestBTWInlineQuestionStartsSideRun(t *testing.T) {
	m := newBTWTestModel(t)
	m.provider = &fakeProvider{}

	side, cmd := m.handleBTWCommand("double-check this assumption")
	if cmd == nil || !side.pending {
		t.Fatalf("inline /btw question did not start a run: pending=%v cmd=%v", side.pending, cmd)
	}
	if side.lastPrompt != "double-check this assumption" {
		t.Fatalf("side last prompt = %q", side.lastPrompt)
	}
	if side.btw.parent == nil || side.btw.parent.pending {
		t.Fatal("idle parent should remain idle while the side run starts")
	}
	parentEvents, err := side.sessionStore.ReadEvents(side.btw.parent.activeSession.SessionID)
	if err != nil {
		t.Fatalf("read parent events: %v", err)
	}
	for _, event := range parentEvents {
		if strings.Contains(string(event.Payload), "double-check this assumption") {
			t.Fatal("inline side question was written into the parent session")
		}
	}
}

func TestBTWRejectsBeforeMainSessionStarts(t *testing.T) {
	m := newModel(context.Background(), Options{SessionStore: sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})})
	got, cmd := m.handleBTWCommand("")
	if cmd != nil {
		t.Fatal("pre-session /btw should not start work")
	}
	if got.btw.active || !transcriptContains(got.transcript, "Start the main session") {
		t.Fatalf("unexpected pre-session /btw result: active=%v transcript=%#v", got.btw.active, got.transcript)
	}
}

func TestBTWBlocksCommandsThatWouldReplaceItsSession(t *testing.T) {
	m := newBTWTestModel(t)
	side, _ := m.handleBTWCommand("")
	updated, _ := side.dispatchCommand(parseCommand("/new"))
	got := updated.(model)
	if !got.btw.active || got.activeSession.SessionKind != sessions.SessionKindSide {
		t.Fatalf("/new replaced the active BTW session: active=%v metadata=%#v", got.btw.active, got.activeSession)
	}
	if !transcriptContains(got.transcript, "unavailable in a BTW conversation") {
		t.Fatalf("missing blocked-command guidance: %#v", got.transcript)
	}
}

func TestBTWBlocksPersistentConfigurationCommands(t *testing.T) {
	for _, input := range []string{
		"/model other",
		"/provider add",
		"/turns 100",
		"/profile fast",
		"/theme dark",
		"/config recaps off",
		"/stt-model",
		"/mcp",
		"/rewind",
		"/compact",
	} {
		t.Run(input, func(t *testing.T) {
			m := newBTWTestModel(t)
			side, _ := m.handleBTWCommand("")
			updated, _ := side.dispatchCommand(parseCommand(input))
			got := updated.(model)
			if !got.btw.active || got.activeSession.SessionKind != sessions.SessionKindSide {
				t.Fatalf("%s escaped the active BTW session: active=%v metadata=%#v", input, got.btw.active, got.activeSession)
			}
			if !transcriptContains(got.transcript, "unavailable in a BTW conversation") {
				t.Fatalf("%s missing blocked-command guidance: %#v", input, got.transcript)
			}
		})
	}
}

func TestBTWAllowsReadOnlyConfigurationCommands(t *testing.T) {
	for _, input := range []string{
		"/model list",
		"/provider status",
		"/turns",
		"/profile status",
		"/theme list",
		"/config",
	} {
		t.Run(input, func(t *testing.T) {
			m := newBTWTestModel(t)
			side, _ := m.handleBTWCommand("")
			updated, _ := side.dispatchCommand(parseCommand(input))
			got := updated.(model)
			if transcriptContains(got.transcript, "unavailable in a BTW conversation") {
				t.Fatalf("%s was blocked even though it is read-only", input)
			}
		})
	}
}

func TestBTWExitBlockedWhileParentRunActive(t *testing.T) {
	m := newBTWTestModel(t)
	m.pending = true
	m.activeRunID = 7
	side, _ := m.handleBTWCommand("")

	updated, cmd := side.dispatchCommand(parseCommand("/exit"))
	got := updated.(model)
	if cmd != nil || got.exiting || !got.btw.active {
		t.Fatalf("/exit escaped BTW while parent was active: cmd=%v exiting=%v active=%v", cmd, got.exiting, got.btw.active)
	}
	if got.btw.parent == nil || !got.btw.parent.pending {
		t.Fatalf("hidden parent run was not preserved: %#v", got.btw.parent)
	}
	if !transcriptContains(got.transcript, "main session is still running") {
		t.Fatalf("missing active-parent exit guidance: %#v", got.transcript)
	}
}

func TestBTWReturnRestartsParentSpinner(t *testing.T) {
	for _, reducedMotion := range []bool{false, true} {
		t.Run(map[bool]string{false: "animated", true: "reduced-motion"}[reducedMotion], func(t *testing.T) {
			m := newBTWTestModel(t)
			m.reducedMotion = reducedMotion
			m.pending = true
			m.spinnerTicking = true
			side, _ := m.handleBTWCommand("")
			side.spinnerTicking = false

			returned, cmd := side.leaveBTW()
			if !returned.pending || !returned.spinnerTicking {
				t.Fatalf("parent spinner was not restarted: pending=%v ticking=%v", returned.pending, returned.spinnerTicking)
			}
			if cmd == nil {
				t.Fatal("returning to an active parent did not schedule a spinner tick")
			}
		})
	}
}

func TestBTWCancelledRunFlushesBeforeReturnAndRunIDsStayUnique(t *testing.T) {
	m := newBTWTestModel(t)
	m.provider = &fakeProvider{}

	side1, _ := m.handleBTWCommand("first side question")
	firstRunID := side1.activeRunID
	side1.cancelRun()
	blocked, _ := side1.leaveBTW()
	if !blocked.btw.active || !transcriptContains(blocked.transcript, "still saving its session events") {
		t.Fatal("BTW returned before the cancelled side run finished flushing")
	}

	flushedModel, _ := side1.updateModel(agentResponseMsg{runID: firstRunID})
	flushed := flushedModel.(model)
	if len(flushed.flushRunIDs) != 0 {
		t.Fatalf("cancelled side run did not drain: %#v", flushed.flushRunIDs)
	}
	parent, _ := flushed.leaveBTW()
	side2, _ := parent.handleBTWCommand("second side question")
	if side2.activeRunID == firstRunID {
		t.Fatalf("BTW run ID was reused across re-entry: %d", firstRunID)
	}

	updated, _ := side2.updateModel(agentResponseMsg{
		runID: firstRunID,
		rows:  []transcriptRow{{kind: rowAssistant, text: "answer to the FIRST question"}},
	})
	got := updated.(model)
	if !got.pending || transcriptContains(got.transcript, "answer to the FIRST question") {
		t.Fatal("a stale side response hijacked the next BTW run")
	}
}

func TestBTWRejectsExplicitResumeOfSideSession(t *testing.T) {
	m := newBTWTestModel(t)
	side, _ := m.handleBTWCommand("")

	if _, err := m.resolveResumeSession(side.activeSession.SessionID); err == nil || !strings.Contains(err.Error(), "not resumable") {
		t.Fatalf("explicit side-session resume error = %v, want non-resumable rejection", err)
	}
}

func TestBTWCannotStartDuringDeferredExit(t *testing.T) {
	m := newBTWTestModel(t)
	m.exiting = true

	got, cmd := m.handleBTWCommand("should not run")
	if cmd != nil || got.btw.active {
		t.Fatalf("BTW started during deferred exit: active=%v cmd=%v", got.btw.active, cmd)
	}
	if !transcriptContains(got.transcript, "cannot start now") {
		t.Fatalf("missing deferred-exit guidance: %#v", got.transcript)
	}
}

func TestBTWStatusAndHelpStayVisible(t *testing.T) {
	m := newBTWTestModel(t)
	side, _ := m.handleBTWCommand("")

	if status := plainRender(t, side.statusLine(100)); !strings.Contains(status, "BTW") {
		t.Fatalf("status line has no persistent BTW indicator: %q", status)
	}
	groups := side.buildKeybindingGroups()
	if got := groups[0].bindings[3].desc; !strings.Contains(got, "return to the main session") {
		t.Fatalf("BTW Ctrl+C help = %q", got)
	}
}

func TestBTWResizeUpdatesHiddenParent(t *testing.T) {
	m := newBTWTestModel(t)
	m.width = 80
	m.height = 24
	side, _ := m.handleBTWCommand("")

	updated, _ := side.updateModel(tea.WindowSizeMsg{Width: 120, Height: 50})
	got := updated.(model)
	if got.btw.parent == nil || got.btw.parent.width != 120 || got.btw.parent.height != 50 {
		t.Fatalf("hidden parent kept stale geometry: %#v", got.btw.parent)
	}
	returned, _ := got.leaveBTW()
	if returned.width != 120 || returned.height != 50 {
		t.Fatalf("restored parent geometry = %dx%d, want 120x50", returned.width, returned.height)
	}
}

func TestBTWUsesIndependentUsageTracker(t *testing.T) {
	m := newBTWTestModel(t)
	parentTracker := m.usageTracker
	side, _ := m.handleBTWCommand("")
	if side.usageTracker == nil || side.usageTracker == parentTracker {
		t.Fatal("BTW conversation shared the main session usage tracker")
	}
	returned, _ := side.leaveBTW()
	if returned.usageTracker != parentTracker {
		t.Fatal("returning from BTW did not restore the main session usage tracker")
	}
}

func TestBTWFailedForkKeepsMainSessionLoops(t *testing.T) {
	badRoot := filepath.Join(t.TempDir(), "sessions")
	if err := os.WriteFile(badRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), Options{SessionStore: sessions.NewStore(sessions.StoreOptions{RootDir: badRoot})})
	m.activeSession = sessions.Metadata{SessionID: "main-session"}
	m.loops = []*loopState{{id: "loop-1"}}

	got, _ := m.handleBTWCommand("")
	if len(got.loops) != 1 || got.loops[0].id != "loop-1" {
		t.Fatalf("failed BTW fork cleared main-session loops: %#v", got.loops)
	}
	if !transcriptContains(got.transcript, "Could not open BTW conversation") {
		t.Fatalf("missing fork failure notice: %#v", got.transcript)
	}
}

func TestBTWCommandReturnsToParentSession(t *testing.T) {
	m := newBTWTestModel(t)
	parentID := m.activeSession.SessionID
	side, _ := m.handleBTWCommand("")

	updated, cmd := side.dispatchCommand(parseCommand("/btw"))
	returned := updated.(model)
	if cmd != nil {
		t.Fatal("returning from an idle BTW conversation should not start work")
	}
	if returned.btw.active || returned.btw.parent != nil {
		t.Fatalf("BTW state remained active after /btw: %#v", returned.btw)
	}
	if returned.activeSession.SessionID != parentID {
		t.Fatalf("returned session = %q, want parent %q", returned.activeSession.SessionID, parentID)
	}
}

func TestBTWCtrlCDuringRunDoesNotClearDraft(t *testing.T) {
	m := newBTWTestModel(t)
	side, _ := m.handleBTWCommand("")
	side.pending = true
	side.input.SetValue("keep this draft")

	updated, cmd := side.handleCtrlC()
	got := updated.(model)
	if cmd != nil {
		t.Fatal("Ctrl+C should not start a command while a BTW response is running")
	}
	if !got.btw.active {
		t.Fatal("Ctrl+C returned from BTW while its response was still running")
	}
	if got.composerValue() != "keep this draft" {
		t.Fatalf("Ctrl+C cleared the in-flight BTW draft: %q", got.composerValue())
	}
	if !transcriptContains(got.transcript, "BTW response is still running") {
		t.Fatalf("missing in-flight return guidance: %#v", got.transcript)
	}
}
