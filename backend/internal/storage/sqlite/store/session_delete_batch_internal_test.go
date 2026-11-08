package store

import (
	"testing"
	"time"
)

// A session delete's batches size themselves from how long the last one held
// the writer, so a table of 64 KiB command outputs gets small batches and a
// table of tiny rows large ones, and no batch runs far past the budget.
func TestSessionDeleteBatchFollowsTheBudget(t *testing.T) {
	cases := []struct {
		batch int
		took  time.Duration
		want  int
	}{
		{32, sessionDeleteBudget + time.Millisecond, 16},                         // over budget: halve
		{sessionDeleteMinBatch, 10 * sessionDeleteBudget, sessionDeleteMinBatch}, // never below the floor
		{32, sessionDeleteBudget / 8, 64},                                        // well under: double
		{sessionDeleteMaxBatch, 0, sessionDeleteMaxBatch},                        // never above the cap
		{32, sessionDeleteBudget / 2, 32},                                        // near the budget: keep
	}
	for _, c := range cases {
		if got := sessionDeleteBatch(c.batch, c.took); got != c.want {
			t.Errorf("sessionDeleteBatch(%d, %v) = %d, want %d", c.batch, c.took, got, c.want)
		}
	}
}
