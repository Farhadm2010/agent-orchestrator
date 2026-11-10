package daemon

import (
	"context"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionfork"
)

type fakeTerminalForkSources struct {
	source sessionfork.Source
	output string
}

func (f fakeTerminalForkSources) TerminalForkSource(context.Context, domain.SessionRecord, string) (sessionfork.Source, error) {
	return f.source, nil
}

func (f fakeTerminalForkSources) ForkTerminalOutput(context.Context, domain.SessionRecord) string {
	return f.output
}

type recordingForkNotes struct {
	source   domain.SessionID
	target   domain.SessionID
	terminal string
}

func (r *recordingForkNotes) ForkHandoffNote(_ context.Context, source domain.SessionRecord, target ports.ForkTarget, terminalOutput string) (string, error) {
	r.source, r.target, r.terminal = source.ID, target.SessionID, terminalOutput
	return "NOTE", nil
}

// A terminal agent with no fork command is forked with its conversation, and
// the end of its screen, given to a fresh agent as text.
func TestTerminalAgentWithoutForkCommandForksAsText(t *testing.T) {
	ctx := context.Background()
	rec := domain.SessionRecord{ID: "copilot-1", Mode: domain.SessionModeTUI, Harness: domain.HarnessCopilot}
	for _, refusal := range []string{sessionfork.CodeAgentUnsupported, sessionfork.CodeNativeMissing} {
		notes := &recordingForkNotes{}
		sources := forkSources{terminal: fakeTerminalForkSources{source: sessionfork.Source{Refusal: refusal}, output: "$ ls"}, notes: notes}
		source, err := sources.ForkSource(ctx, rec, "")
		if err != nil {
			t.Fatal(err)
		}
		if source.Refusal != "" || source.ContextMode != domain.ForkContextTextHandoff || source.Handoff == nil {
			t.Fatalf("%s: source = %+v, want a text handoff", refusal, source)
		}
		note, err := source.Handoff(ctx, ports.ForkTarget{SessionID: "fork-1"})
		if err != nil || note != "NOTE" {
			t.Fatalf("%s: handoff = %q, %v", refusal, note, err)
		}
		if notes.source != "copilot-1" || notes.target != "fork-1" || notes.terminal != "$ ls" {
			t.Fatalf("%s: note built from %+v, want the original, the fork, and its screen", refusal, notes)
		}
	}
}

// Other refusals still stand: a text copy can't fix them.
func TestTerminalForkKeepsRefusalsTextCantFix(t *testing.T) {
	rec := domain.SessionRecord{ID: "copilot-1", Mode: domain.SessionModeTUI}
	sources := forkSources{terminal: fakeTerminalForkSources{source: sessionfork.Source{Refusal: sessionfork.CodeOlderTurn}}, notes: &recordingForkNotes{}}
	source, err := sources.ForkSource(context.Background(), rec, "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if source.Refusal != sessionfork.CodeOlderTurn {
		t.Fatalf("refusal = %q, want %q", source.Refusal, sessionfork.CodeOlderTurn)
	}
}

// A session whose chat agent isn't running still learns its PR moved: its
// agent is started, as opening the session would, and then told.
func TestPRMovedNoticeStartsAChatAgentThatIsntRunning(t *testing.T) {
	var started bool
	var sent string
	send := func(_ context.Context, _ domain.SessionID, text string) error {
		if !started {
			return fmt.Errorf("send x: %w", chatsvc.ErrNoController)
		}
		sent = text
		return nil
	}
	resume := func(context.Context, domain.SessionID) error { started = true; return nil }
	if err := tellPRMoved(context.Background(), send, resume, "fork-1", "PR #1 moved"); err != nil {
		t.Fatal(err)
	}
	if !started || sent != "PR #1 moved" {
		t.Fatalf("started = %v, sent = %q; want the agent started and told", started, sent)
	}
}

func TestPRMovedNoticeIsSentToARunningAgent(t *testing.T) {
	var sent string
	send := func(_ context.Context, _ domain.SessionID, text string) error { sent = text; return nil }
	resume := func(context.Context, domain.SessionID) error {
		t.Fatal("a running agent must not be restarted")
		return nil
	}
	if err := tellPRMoved(context.Background(), send, resume, "fork-1", "PR #1 moved"); err != nil || sent != "PR #1 moved" {
		t.Fatalf("sent = %q, %v", sent, err)
	}
}
