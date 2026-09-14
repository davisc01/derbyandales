package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davisc01/derbyandales/internal/store"
)

// A car gets one championship. These drive the import of the club's own
// archive, and the matching that turns it into something useful at the table.

// archiveSite builds a site folder holding the club's real published
// championship standings, copied from testdata.
func archiveSite(t *testing.T, a *App) string {
	t.Helper()
	root := t.TempDir()
	for _, year := range []string{"2019", "2021", "2022", "2023", "2024", "2025"} {
		dir := filepath.Join(root, "content", "races", year, "championship")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join("..", "..", "testdata", "championships", year+"-standings.csv"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "standings.csv"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.DB.SetSetting(context.Background(), store.KeyDerbySitePath, root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTheClubsOwnArchiveImports(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	archiveSite(t, a)

	years, err := a.ImportChampionships(ctx)
	if err != nil {
		t.Fatalf("ImportChampionships: %v", err)
	}
	if len(years) != 6 {
		t.Fatalf("%d championships found, want 6", len(years))
	}
	for _, y := range years {
		if y.Problem != "" {
			t.Errorf("%d could not be read: %s", y.Year, y.Problem)
		}
		if y.Cars < 15 {
			t.Errorf("%d imported %d cars", y.Year, y.Cars)
		}
	}

	onRecord, err := a.DB.ChampionshipYears(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(onRecord) != 6 {
		t.Errorf("%d years on record, want 6", len(onRecord))
	}
	if onRecord[0] != 2025 {
		t.Errorf("years come back starting at %d, want the newest first", onRecord[0])
	}

	cars, _ := a.DB.PastChampionshipCars(ctx)
	if cars < 110 {
		t.Errorf("%d cars on record across six championships", cars)
	}

	// Running it again replaces rather than doubles: the archive gets corrected.
	if _, err := a.ImportChampionships(ctx); err != nil {
		t.Fatal(err)
	}
	again, _ := a.DB.PastChampionshipCars(ctx)
	if again != cars {
		t.Errorf("a second import changed the count from %d to %d", cars, again)
	}
}

// The strong case: the same person bringing back the same car.
func TestTheSameCarAndDriverIsFlagged(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	archiveSite(t, a)
	if _, err := a.ImportChampionships(ctx); err != nil {
		t.Fatal(err)
	}

	// From the 2025 championship: Michael Sanders, "The Pelvinator".
	seen, err := a.DB.RacedBefore(ctx, "Sanders", "The Pelvinator")
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("a car that won the 2025 championship was not recognised")
	}
	if !seen[0].SameDriver {
		t.Error("the match is not marked as the same driver")
	}
	if seen[0].Year != 2025 {
		t.Errorf("matched %d, want 2025", seen[0].Year)
	}

	// The same car typed differently still matches: people are not consistent
	// about capitals and spaces.
	for _, spelling := range []string{"the pelvinator", "  The  Pelvinator ", "THE PELVINATOR"} {
		again, _ := a.DB.RacedBefore(ctx, "Sanders", spelling)
		if len(again) == 0 {
			t.Errorf("%q did not match", spelling)
		}
	}
}

// 2021's exporter appended the qualifying origin to every car name. A car from
// that year has to match under its real name.
func TestACarFromTheYearWithSuffixesStillMatches(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	archiveSite(t, a)
	a.ImportChampionships(ctx)

	// Published as "Red Rocket-1" in 2021.
	seen, err := a.DB.RacedBefore(ctx, "Lloyd", "Red Rocket")
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal(`"Red Rocket" did not match the 2021 entry published as "Red Rocket-1"`)
	}
	if !seen[0].SameDriver || seen[0].Year != 2021 {
		t.Errorf("matched %+v", seen[0])
	}
}

// A different person with a car of the same name is a weaker claim, and has to
// read as one — otherwise the strong case stops meaning anything.
func TestADifferentDriverIsAWeakerMatch(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	archiveSite(t, a)
	a.ImportChampionships(ctx)

	seen, err := a.DB.RacedBefore(ctx, "Nobody", "The Pelvinator")
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("the car was not found at all")
	}
	if seen[0].SameDriver {
		t.Error("a different driver was reported as the same one")
	}
}

// A car nobody has raced before must not be flagged, or the flag is noise.
func TestANewCarIsNotFlagged(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	archiveSite(t, a)
	a.ImportChampionships(ctx)

	for _, name := range []string{"Brand New Thing", "", "   "} {
		seen, err := a.DB.RacedBefore(ctx, "Fairweather", name)
		if err != nil {
			t.Fatal(err)
		}
		if len(seen) != 0 {
			t.Errorf("%q was flagged: %+v", name, seen)
		}
	}
}

// Without a website folder there is nothing to import, and the message has to
// say that rather than leaking a path error.
func TestImportingWithoutTheWebsiteFolderIsExplained(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	_, err := a.ImportChampionships(ctx)
	if err == nil {
		t.Fatal("importing with no website folder was allowed")
	}
	if strings.Contains(err.Error(), "no such file") {
		t.Errorf("error leaks a system message: %s", err)
	}
}

// A year whose file cannot be read is reported, not skipped. A missing year
// means cars that will not be flagged, and nobody would notice.
func TestAnUnreadableYearIsReportedRatherThanSkipped(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	root := archiveSite(t, a)

	broken := filepath.Join(root, "content", "races", "2022", "championship", "standings.csv")
	if err := os.WriteFile(broken, []byte("this is not a csv table\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	years, err := a.ImportChampionships(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var problem string
	for _, y := range years {
		if y.Year == 2022 {
			problem = y.Problem
		}
	}
	if problem == "" {
		t.Fatal("a year that could not be read was reported as fine")
	}
	// And the other five still went in.
	if cars, _ := a.DB.PastChampionshipCars(ctx); cars < 90 {
		t.Errorf("one bad year cost the whole import: %d cars", cars)
	}
}
