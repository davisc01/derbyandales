package bracket

import (
	"strings"
	"testing"
)

// The planner's figures were worked out by hand against the club's actual
// season before any of this was written. If they stop matching, the arithmetic
// has drifted.
func TestThePlannerMatchesTheWorkedTable(t *testing.T) {
	cases := []struct {
		races, wildcards                             int
		entrants, capacity, byes, firstRound, rounds int
	}{
		{4, 4, 16, 16, 0, 8, 4},
		{5, 1, 16, 16, 0, 8, 4},
		{6, 6, 24, 32, 8, 8, 5}, // the club today
		{6, 14, 32, 32, 0, 16, 5},
		{7, 11, 32, 32, 0, 16, 5},
		{8, 8, 32, 32, 0, 16, 5},
	}
	for _, c := range cases {
		p := PlanFor(c.races, 3, c.wildcards)
		if p.Entrants != c.entrants || p.Capacity != c.capacity || p.Byes != c.byes ||
			p.FirstRound != c.firstRound || p.Rounds != c.rounds {
			t.Errorf("%d races + %d wildcards = %d entrants, %d capacity, %d byes, %d first-round, %d rounds;"+
				" want %d, %d, %d, %d, %d",
				c.races, c.wildcards, p.Entrants, p.Capacity, p.Byes, p.FirstRound, p.Rounds,
				c.entrants, c.capacity, c.byes, c.firstRound, c.rounds)
		}
	}
}

// The club's own shape must not produce a warning, or the warning means nothing.
func TestTheClubsCurrentShapeIsNotWarnedAbout(t *testing.T) {
	p := PlanFor(6, 3, 6)
	if len(p.Warnings) != 0 {
		t.Errorf("the club's own championship was warned about: %v", p.Warnings)
	}
	if p.ByeFree() {
		t.Error("a 24-car field in a 32 bracket was reported as bye-free")
	}
}

// A field that is nearly all byes is legal and useless: seventeen cars in a
// thirty-two bracket would race one matchup in round one.
func TestAFieldThatIsMostlyByesIsWarnedAbout(t *testing.T) {
	p := PlanFor(5, 3, 2) // 17 entrants, capacity 32, 15 byes, 1 first-round race
	if p.FirstRound != 1 {
		t.Fatalf("first-round races = %d, want 1", p.FirstRound)
	}
	if len(p.Warnings) == 0 {
		t.Fatal("no warning about a field that is almost entirely byes")
	}
}

// Byes are meant to reward a good regular season. If there are more byes than
// auto-qualifiers, they start landing on wildcard racers instead.
func TestByesReachingWildcardRacersIsWarnedAbout(t *testing.T) {
	// 2 races x top 3 = 6 qualifiers, plus 3 wildcards = 9 entrants in a
	// 16 bracket: 7 byes, still under the 6 qualifiers? No - 7 > 6.
	p := PlanFor(2, 3, 3)
	if p.Byes <= p.Races*3 {
		t.Fatalf("this shape does not exercise the warning: %d byes, %d qualifiers",
			p.Byes, p.Races*3)
	}
	found := false
	for _, w := range p.Warnings {
		if strings.Contains(w, "wildcard") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning that byes would reach wildcard racers: %v", p.Warnings)
	}
}

// The question a coordinator actually asks when the season changes.
func TestSuggestingWildcardsForAShorterSeason(t *testing.T) {
	got := SuggestWildcards(5, 3, 20)
	if len(got) == 0 {
		t.Fatal("no workable wildcard counts for a five-race season")
	}
	// A five-race season is cleanest at one wildcard: a bye-free sixteen.
	var byeFree []int
	for _, p := range got {
		if p.ByeFree() {
			byeFree = append(byeFree, p.Wildcards)
		}
	}
	if len(byeFree) == 0 {
		t.Fatal("no bye-free option offered for five races")
	}
	if byeFree[0] != 1 {
		t.Errorf("smallest bye-free option is %d wildcards, want 1", byeFree[0])
	}
	// Every suggestion has to be one that would not be warned about.
	for _, p := range got {
		if len(p.Warnings) != 0 {
			t.Errorf("%d wildcards was suggested but warns: %v", p.Wildcards, p.Warnings)
		}
	}
}
