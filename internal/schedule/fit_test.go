package schedule

import (
	"reflect"
	"testing"
)

// split generates a night and divides it at a heat, as if racing had reached
// that point when somebody turned up late.
func split(t *testing.T, cars, at int) (run, remaining []Heat) {
	t.Helper()
	s, err := Generate(cars, 4)
	if err != nil {
		t.Fatal(err)
	}
	return s.Heats[:at], s.Heats[at:]
}

func byes(heats []Heat) int {
	n := 0
	for _, h := range heats {
		for _, car := range h {
			if car == Bye {
				n++
			}
		}
	}
	return n
}

// The club's rule holds over the whole night after a late car is fitted in —
// the cars already racing keep exactly the lanes they were owed, and the late
// ones get all four. Fit validates its own output, so any failure surfaces as
// an error here, across every field and every point in the night.
func TestFittingLateCarsKeepsEveryCarOncePerLane(t *testing.T) {
	for cars := 4; cars <= 30; cars++ {
		for late := 1; late <= 4; late++ {
			// Every field size, and split points across the night; every
			// single split point too makes this the slowest test under -race.
			for at := 0; at < cars; at += 1 + cars/8 {
				run, remaining := split(t, cars, at)
				if _, err := Fit(run, remaining, cars, late, 4); err != nil {
					t.Fatalf("cars=%d late=%d at=%d: %v", cars, late, at, err)
				}
			}
		}
	}
}

// With enough of the night left, a late car takes a full track alongside the
// others: one extra heat per late car and not one empty lane. That is the case
// the coordinator will see almost every time, and an empty lane where none was
// needed is a run nobody gets.
func TestALateCarBlendsInWithNoEmptyLanesWhenEnoughHeatsRemain(t *testing.T) {
	run, remaining := split(t, 20, 8)
	for late := 1; late <= 3; late++ {
		got, err := Fit(run, remaining, 20, late, 4)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(remaining)+late {
			t.Errorf("late=%d: %d heats, want %d", late, len(got), len(remaining)+late)
		}
		if b := byes(got); b != 0 {
			t.Errorf("late=%d: %d empty lanes, want none", late, b)
		}
	}
}

// Near the end of the night there are not enough runs owed to fill four heats,
// and the late car still needs four. Those heats go ahead with empty lanes
// rather than borrowing a run from a car that has finished.
func TestALateCarNearTheEndRunsAgainstEmptyLanes(t *testing.T) {
	run, remaining := split(t, 20, 19) // one heat left
	got, err := Fit(run, remaining, 20, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("%d heats, want 4 — one per lane for the late car", len(got))
	}
	// 16 lanes, 4 for the late car and 4 for the cars in the last heat.
	if b := byes(got); b != 8 {
		t.Errorf("%d empty lanes, want 8", b)
	}

	// With nothing left at all, the late car runs alone.
	all, _ := split(t, 20, 20)
	got, err = Fit(all, nil, 20, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || byes(got) != 12 {
		t.Errorf("got %d heats with %d empty lanes, want 4 with 12", len(got), byes(got))
	}
}

// The new block is ordered knowing what just ran: a car in the last heat raced
// must not be sent straight back out, and a lone late car's four runs must not
// come one after another. Either is a car that cannot get back to the start.
func TestFittedHeatsAvoidBackToBackAcrossTheJoin(t *testing.T) {
	for _, at := range []int{3, 8, 12} {
		run, remaining := split(t, 20, at)
		got, err := Fit(run, remaining, 20, 1, 4)
		if err != nil {
			t.Fatal(err)
		}
		all := append(append([]Heat{}, run...), got...)
		if g := MinGap(&Schedule{Cars: 21, Heats: all}); g < 2 {
			t.Errorf("at=%d: some car races in back-to-back heats", at)
		}
	}
}

// The late car's runs are spread through what is left rather than bunched at
// the front of it — what the coordinator was told would happen.
func TestALateCarsRunsAreSpreadThroughTheRestOfTheNight(t *testing.T) {
	run, remaining := split(t, 24, 4)
	got, err := Fit(run, remaining, 24, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	var at []int
	for h, heat := range got {
		for _, car := range heat {
			if car == 24 {
				at = append(at, h)
			}
		}
	}
	if len(at) != 4 {
		t.Fatalf("late car runs %d times, want 4", len(at))
	}
	if at[3] < len(got)/2 {
		t.Errorf("late car's runs are at %v of %d heats — all in the first half", at, len(got))
	}
}

// Asking twice gives the same heats, so the dry run on race control describes
// exactly what pressing the button does.
func TestFittingIsReproducible(t *testing.T) {
	run, remaining := split(t, 17, 6)
	a, err := Fit(run, remaining, 17, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Fit(run, remaining, 17, 2, 4)
	if !reflect.DeepEqual(a, b) {
		t.Error("two fits of the same night differ")
	}
}
