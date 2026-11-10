package session

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPRMovedAwayNotifiesSource(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	st.pr["mer-1"] = domain.PRFacts{URL: "https://github.com/acme/repo/pull/7", Number: 7, CI: domain.CIPassing}
	obs := ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7}}
	type moved struct {
		from, to domain.SessionID
		number   int
	}
	var got []moved
	notify := func(_ context.Context, from, to domain.SessionID, pr domain.PullRequest) {
		got = append(got, moved{from, to, pr.Number})
	}

	svc := NewWithDeps(Deps{Store: st, PRClaimer: &fakePRClaimer{out: errorFreeClaimOutcome{ports.ClaimOutcome{PreviousOwner: "mer-2"}}}, SCM: fakeSCM{obs: obs}})
	svc.SetPRMovedNotifier(notify)
	if _, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{AllowTakeover: true}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (moved{"mer-2", "mer-1", 7}) {
		t.Fatalf("notified = %+v", got)
	}

	// A terminated previous owner has nobody to tell.
	got = nil
	svc = NewWithDeps(Deps{Store: st, PRClaimer: &fakePRClaimer{out: errorFreeClaimOutcome{ports.ClaimOutcome{PreviousOwner: "mer-2", OwnerTerminated: true}}}, SCM: fakeSCM{obs: obs}})
	svc.SetPRMovedNotifier(notify)
	if _, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{AllowTakeover: true}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("notified a terminated owner: %+v", got)
	}
}
