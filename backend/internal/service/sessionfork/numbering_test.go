package sessionfork

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// lockedLineage is fakeLineage safe for concurrent forks.
type lockedLineage struct {
	mu sync.Mutex
	fakeLineage
}

func (l *lockedLineage) RecordSessionFork(ctx context.Context, fork domain.SessionFork) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeLineage.RecordSessionFork(ctx, fork)
}
func (l *lockedLineage) GetSessionFork(ctx context.Context, id domain.SessionID) (domain.SessionFork, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeLineage.GetSessionFork(ctx, id)
}
func (l *lockedLineage) NextSessionForkNumber(ctx context.Context, root domain.SessionID) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeLineage.NextSessionForkNumber(ctx, root)
}
func (l *lockedLineage) ListSessionForks(ctx context.Context, id domain.SessionID) ([]domain.SessionFork, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeLineage.ListSessionForks(ctx, id)
}

// gatedSpawner holds every spawn until all of them have started, the way two
// forks overlap while each one's worktree is being set up.
type gatedSpawner struct {
	mu      sync.Mutex
	names   []string
	started sync.WaitGroup
}

func (g *gatedSpawner) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	g.mu.Lock()
	g.names = append(g.names, cfg.DisplayName)
	n := len(g.names)
	g.mu.Unlock()
	g.started.Done()
	g.started.Wait()
	return domain.SessionRecord{ID: domain.SessionID(fmt.Sprintf("p-f%d", n)), DisplayName: cfg.DisplayName}, 0, 0, nil
}

// Forks of one session started at the same time each get their own number.
func TestForksStartedTogetherGetTheirOwnNumbers(t *testing.T) {
	spawner := &gatedSpawner{}
	spawner.started.Add(2)
	lineage := &lockedLineage{}
	s := New(Deps{
		Enabled:  true,
		Sessions: fakeSessions{source().ID: source()},
		Projects: fakeProjects{"p": {ID: "p", Kind: domain.ProjectKindSingleRepo}},
		Lineage:  lineage, Spawner: spawner, Sources: fakeSources{latestTurn: "turn-4"}, Seeds: fakeSeeds{},
	})
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Fork(context.Background(), "p-1", fmt.Sprintf("req-%d", i)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(spawner.names) != 2 || spawner.names[0] == spawner.names[1] {
		t.Fatalf("names = %q, want two different names", spawner.names)
	}
	numbers := map[int]bool{}
	for _, row := range lineage.rows {
		numbers[row.ForkNumber] = true
	}
	if len(numbers) != 2 {
		t.Fatalf("recorded numbers = %v, want two different ones", lineage.rows)
	}
	// The next fork follows both.
	opts, err := s.Options(context.Background(), "p-1")
	if err != nil || opts.SuggestedName != "Fork 3 of Retry failed webhook deliveries" {
		t.Fatalf("next name = %q, %v", opts.SuggestedName, err)
	}
}
