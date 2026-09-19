package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/davisc01/derbyandales/internal/records"
	"github.com/davisc01/derbyandales/internal/store"
)

// The club records run through six years of the club's archive and on into the
// nights raced here. These drive the whole path: reading the archive, joining
// it to this software's own races, and noticing a record fall as a heat lands.

// archiveRaces builds a site folder holding real published race nights,
// copied from testdata.
func archiveRaces(t *testing.T, a *App) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("..", "..", "testdata", "races")
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		dst := filepath.Join(root, "content", "races", rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, body, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.DB.SetSetting(context.Background(), store.KeyDerbySitePath, root); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ImportRaces(context.Background()); err != nil {
		t.Fatalf("ImportRaces: %v", err)
	}
}

// realSeason turns the demo fixture's season into an ordinary one, since the
// demo season is deliberately kept out of the records.
func realSeason(t *testing.T, a *App, raceID int64, year int) {
	t.Helper()
	ctx := context.Background()
	race, err := a.DB.Race(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	season, err := a.DB.Season(ctx, race.SeasonID)
	if err != nil {
		t.Fatal(err)
	}
	season.Name = "A real season"
	season.Year = year
	if err := a.DB.UpdateSeason(ctx, season); err != nil {
		t.Fatal(err)
	}
}

func TestTheArchiveImportsEveryRaceNight(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	archiveRaces(t, a)

	races, runs, err := a.DB.ArchiveRaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if races != 6 || runs < 400 {
		t.Errorf("%d races and %d runs on record", races, runs)
	}

	// Reading it again replaces rather than doubles: the archive gets corrected.
	if _, err := a.ImportRaces(ctx); err != nil {
		t.Fatal(err)
	}
	if _, again, _ := a.DB.ArchiveRaces(ctx); again != runs {
		t.Errorf("a second import changed the run count from %d to %d", runs, again)
	}
}

// The club's published records, recomputed from the heat files: Greg Thrift's
// 2.292 in 2025 race 1 is the fastest run on file.
func TestTheRecordsComeFromTheArchive(t *testing.T) {
	a := testApp(t)
	archiveRaces(t, a)

	book, err := a.Records(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r := book.FastestRun; r == nil || r.Driver != "Greg Thrift" || r.Time != 2.292 {
		t.Fatalf("fastest run = %+v", book.FastestRun)
	}
	if len(book.Lanes) != 4 {
		t.Errorf("%d lane records", len(book.Lanes))
	}
	if book.FastestAverage == nil || book.FastestAverage.Race != (records.Race{Year: 2025, Number: 1}) {
		t.Errorf("fastest average = %+v", book.FastestAverage)
	}
}

// Car 901 ran the fastest average of 2026 race 4 and was ruled out. It raced,
// so it is on file — but it holds nothing, not even its driver's best.
func TestAnExcludedCarHoldsNoRecord(t *testing.T) {
	a := testApp(t)
	archiveRaces(t, a)

	book, err := a.Records(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range book.PersonalBests {
		if r.Driver == "Justin Palmer" && r.Race == (records.Race{Year: 2026, Number: 4}) {
			t.Errorf("Justin Palmer's best is from a race both his cars were excluded from: %+v", r)
		}
	}
	for _, r := range book.PersonalBests {
		if r.Driver == "Derby Ales" {
			t.Error("the pace car's driver has a personal best")
		}
	}
}

// A night raced here is read from here. Once published, the site has a copy,
// and reading both would put every run on file twice — so the archive's copy
// of a night this software raced is left out.
func TestARaceRunHereReplacesTheArchivesCopy(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	archiveRaces(t, a)
	realSeason(t, a, raceID, 2025) // the demo race is race 1: now 2025 race 1

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	enterHeat(t, a, raceID, 0, 2.45)

	book, err := a.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if book.FastestRun.Driver == "Greg Thrift" {
		t.Error("2025 race 1 was counted from the archive as well as from here")
	}
}

// The moment the feature exists for: a heat lands under the track record, and
// the screen can say so, with what it beat.
func TestAHeatUnderTheTrackRecordIsAnnounced(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	archiveRaces(t, a)
	realSeason(t, a, raceID, 2027)

	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	heat := enterHeat(t, a, raceID, 0, 2.200)

	notices, err := a.HeatRecords(ctx, heat)
	if err != nil {
		t.Fatal(err)
	}
	var track []records.Notice
	for _, l := range heat.Lanes {
		if n, ok := notices[l.Lane]; ok && n.Kind == records.TrackRecord {
			track = append(track, n)
		}
	}
	if len(track) != 1 {
		t.Fatalf("%d track records from one heat, want 1: %+v", len(track), notices)
	}
	if track[0].Previous.Driver != "Greg Thrift" || track[0].Previous.Time != 2.292 {
		t.Errorf("it beat %+v", track[0].Previous)
	}
}

// The demo season's times are invented. Left in, a rehearsal would set records
// that then sit on the real record book.
func TestTheDemoSeasonIsNotInTheRecords(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	archiveRaces(t, a)
	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	enterHeat(t, a, raceID, 0, 2.100)

	d, err := a.DB.RecordData(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	book := records.Compute(d.Runs, d.Results)
	if book.FastestRun.Time == 2.100 {
		t.Error("an invented demo time is on the record book")
	}

	// Rehearsing on the demo, though, the notices have to show, or they would
	// never be seen before a real race night.
	if err := a.Race.SetRace(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	heats, _ := a.DB.Heats(ctx, raceID)
	notices, err := a.HeatRecords(ctx, heats[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) == 0 {
		t.Error("a record broken in a demo race was not shown while rehearsing")
	}
}

// enterHeat types one heat's times in by hand, the fastest car at best and the
// rest a little behind, and returns the heat as recorded.
func enterHeat(t *testing.T, a *App, raceID int64, index int, best float64) store.HeatView {
	t.Helper()
	ctx := context.Background()
	heats, err := a.DB.Heats(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	heat := heats[index]
	times := map[int]float64{}
	for i, l := range heat.Lanes {
		if l.EntryID != nil {
			times[l.Lane] = best + float64(i)*0.001
		}
	}
	if err := a.Race.EnterTimes(ctx, heat.ID, times, "coordinator"); err != nil {
		t.Fatalf("EnterTimes: %v", err)
	}
	after, err := a.DB.Heat(ctx, heat.ID)
	if err != nil {
		t.Fatal(err)
	}
	return after
}

// The wrap-up's timer health counts re-runs and hand-typed heats against the
// race they happened in. The audit log says only "heat 3", which cannot.
func TestReRunsAndTypedHeatsAreCountedAgainstTheirRace(t *testing.T) {
	a, raceID := raceFixture(t)
	ctx := context.Background()
	if _, err := a.Race.GenerateSchedule(ctx, raceID); err != nil {
		t.Fatal(err)
	}
	heat := enterHeat(t, a, raceID, 0, 2.40)
	if err := a.Race.ReRun(ctx, heat.ID); err != nil {
		t.Fatalf("ReRun: %v", err)
	}

	events, err := a.DB.HeatEvents(ctx, raceID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []store.HeatEventKind
	for _, e := range events {
		if e.Heat != heat.Number {
			t.Errorf("event for heat %d, want %d", e.Heat, heat.Number)
		}
		kinds = append(kinds, e.Kind)
	}
	if len(kinds) != 2 || kinds[0] != store.HeatManual || kinds[1] != store.HeatReRun {
		t.Errorf("events = %v, want typed in then re-run", kinds)
	}
}

// The CONTROL car's nights come from the archive as well as from here, one per
// night, and never the excluded second CONTROL car some nights had.
func TestTheControlCarHasANightForEveryArchivedRace(t *testing.T) {
	a := testApp(t)
	archiveRaces(t, a)
	nights, err := a.DB.ControlNights(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	// 2019 race 1 and the 2025 championship ran without one; the other four
	// fixtures had one.
	if len(nights) != 4 {
		t.Errorf("%d CONTROL nights, want 4: %+v", len(nights), nights)
	}
	for i := 1; i < len(nights); i++ {
		if !nights[i-1].Race.Before(nights[i].Race) {
			t.Error("CONTROL nights are not oldest first")
		}
	}
	for _, n := range nights {
		if n.Average < 2.3 || n.Average > 3.6 {
			t.Errorf("%s: CONTROL average %v", n.Race.Label(), n.Average)
		}
	}
}
