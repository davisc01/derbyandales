package app

import (
	"context"
	"testing"
)

// The run-off is otherwise never seen in a demo: random times do not tie.
func TestADemoTieProducesATieForFirst(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	runWholeRace(t, a, raceID, 0, 0)

	if err := a.ForceDemoTie(ctx, raceID); err != nil {
		t.Fatalf("ForceDemoTie: %v", err)
	}
	ties, err := a.DB.UnsettledTies(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ties) != 1 || ties[0].Place != 1 || len(ties[0].Entries) != 2 {
		t.Fatalf("ties after staging one: %+v", ties)
	}
}
