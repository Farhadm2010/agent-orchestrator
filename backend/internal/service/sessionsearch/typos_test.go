package sessionsearch_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
)

// typoFixture indexes one chat session whose messages hold the words the
// typo tests misspell.
func typoFixture(t *testing.T) (fixture, string) {
	t.Helper()
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-typos", "conv-typos")
	appendUserMessage(t, f.store, session, "conv-typos", "msg-1", "turn-1",
		"squash the user table migrations before the release")
	appendUserMessage(t, f.store, session, "conv-typos", "msg-2", "turn-2",
		"docker-compose.yml needs the notarize step for the build")
	appendUserMessage(t, f.store, session, "conv-typos", "msg-3", "turn-3",
		"the build runs fine with the cache")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	return f, string(session.ID)
}

// Found by the Orca benchmark: one swapped letter cost 5 to 11 points of
// "right session first", and 30% of misspelled coding queries found nothing.
func TestAMisspelledWordIsSearchedAsTheClosestIndexedWord(t *testing.T) {
	f, _ := typoFixture(t)
	cases := []struct{ typed, from, to string }{
		{"migartions", "migartions", "migrations"},
		// An identifier keeps its other parts as typed.
		{"dokcer-compose.yml", "dokcer-compose.yml", "docker-compose.yml"},
		// Half-typed and misspelled: compared with the start of longer words.
		{"noatriz", "noatriz", "notarize"},
	}
	for _, c := range cases {
		response := search(t, f.svc, c.typed)
		if len(response.Results) != 1 {
			t.Fatalf("%q: results = %s, want the session", c.typed, describe(response.Results))
		}
		want := []domain.SessionSearchCorrection{{From: c.from, To: c.to}}
		if len(response.Corrections) != 1 || response.Corrections[0] != want[0] {
			t.Fatalf("%q: corrections = %+v, want %+v", c.typed, response.Corrections, want)
		}
	}
}

// A word that matches as typed (or by its stem, or as a prefix) is never
// "corrected" into a neighbour, and short words are left alone.
func TestAWordThatMatchesIsNotCorrected(t *testing.T) {
	f, _ := typoFixture(t)
	for _, typed := range []string{"migration", "migr", "build", "cahce"} {
		if got := search(t, f.svc, typed).Corrections; len(got) != 0 {
			t.Fatalf("%q was corrected to %+v", typed, got)
		}
	}
}

// A result from a corrected search marks the corrected word: the opened
// session starts find in chat on the marked word, so it must not be the typo
// that matched nothing.
func TestACorrectedResultMarksTheCorrectedWord(t *testing.T) {
	f, sessionID := typoFixture(t)
	got := search(t, f.svc, "migartions")
	if len(got.Corrections) != 1 || len(got.Results) == 0 || got.Results[0].SessionID != sessionID || got.Results[0].MessageID != "msg-1" {
		t.Fatalf("corrections=%+v results=%+v, want msg-1 found through the correction", got.Corrections, got.Results)
	}
	top := got.Results[0]
	if len(top.Highlights) == 0 {
		t.Fatalf("result %+v marks nothing", top)
	}
	marked := top.Snippet[top.Highlights[0].Start:top.Highlights[0].End]
	if !strings.EqualFold(marked, "migrations") {
		t.Fatalf("marked %q in %q, want the corrected word", marked, top.Snippet)
	}
}
