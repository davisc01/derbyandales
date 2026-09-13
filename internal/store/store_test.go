package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/model"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite3")
	ctx := context.Background()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	db.Close()

	// Reopening must not try to re-apply anything.
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer db.Close()

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migration`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("applied migrations = %d, want 1", n)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	db := testDB(t)
	// Without PRAGMA foreign_keys=ON this would silently succeed and leave an
	// orphan race that nothing would ever clean up.
	_, err := db.Exec(`INSERT INTO race (season_id, number, name, date, venue, kind, status, created_at)
	                   VALUES (9999, 1, 'Orphan', 0, '', 'points', 'setup', 0)`)
	if err == nil {
		t.Fatal("expected a foreign key violation")
	}
}

func TestDefaultSeasonMatchesClubRules(t *testing.T) {
	s := DefaultSeason(2027)
	// These are the club's actual rules; a change here changes the championship
	// field size, so it should be a deliberate edit rather than a drifting default.
	if s.RaceCount != 6 || s.AutoQualPlaces != 3 || s.WildcardSpots != 6 {
		t.Errorf("season shape = %d races / top %d / %d wildcards, want 6/3/6",
			s.RaceCount, s.AutoQualPlaces, s.WildcardSpots)
	}
	if s.LaneCount != 4 || s.TrackLengthFt != 28 || s.ScaleDenom != 25 {
		t.Errorf("track = %d lanes / %.0f ft / 1:%d, want 4/28/25",
			s.LaneCount, s.TrackLengthFt, s.ScaleDenom)
	}
	if s.PointsCountControl {
		t.Error("the CONTROL car must not inflate the field size by default")
	}
}

func TestSeasonRoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	in := DefaultSeason(2027)
	in.Name = "2027 Season"
	created, err := db.CreateSeason(ctx, in)
	if err != nil {
		t.Fatalf("CreateSeason: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("CreateSeason returned no id")
	}

	got, err := db.Season(ctx, created.ID)
	if err != nil {
		t.Fatalf("Season: %v", err)
	}
	if got.Year != 2027 || got.Name != "2027 Season" {
		t.Errorf("got %+v", got)
	}
	if got.WildcardSpots != 6 || got.BracketLaneA != 1 || got.BracketLaneB != 2 {
		t.Errorf("season fields did not round-trip: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt did not round-trip")
	}
}

func TestSeasonBooleanRoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	s := DefaultSeason(2027)
	s.PointsCountControl = true
	s, err := db.CreateSeason(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.Season(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.PointsCountControl {
		t.Error("PointsCountControl=true did not survive a round trip")
	}

	got.PointsCountControl = false
	if err := db.UpdateSeason(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, _ = db.Season(ctx, s.ID)
	if got.PointsCountControl {
		t.Error("PointsCountControl=false did not survive an update")
	}
}

func TestSeasonNotFound(t *testing.T) {
	db := testDB(t)
	if _, err := db.Season(context.Background(), 404); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestRacesOrderPointsBeforeChampionship(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	season, err := db.CreateSeason(ctx, DefaultSeason(2027))
	if err != nil {
		t.Fatal(err)
	}

	// Insert out of order, including the championship first.
	if _, err := db.CreateRace(ctx, model.Race{
		SeasonID: season.ID, Number: 1, Name: "Championship",
		Kind: model.RaceChampionship, Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{3, 1, 2} {
		if _, err := db.CreateRace(ctx, model.Race{
			SeasonID: season.ID, Number: n, Name: "Race", Date: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	races, err := db.Races(ctx, season.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(races) != 4 {
		t.Fatalf("got %d races, want 4", len(races))
	}
	for i, want := range []int{1, 2, 3} {
		if races[i].Kind != model.RacePoints || races[i].Number != want {
			t.Errorf("races[%d] = %s %d, want points %d", i, races[i].Kind, races[i].Number, want)
		}
	}
	if races[3].Kind != model.RaceChampionship {
		t.Errorf("championship should sort last, got %s", races[3].Kind)
	}
}

func TestRaceNumberIsUniquePerKind(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	season, _ := db.CreateSeason(ctx, DefaultSeason(2027))

	mk := func(kind model.RaceKind) error {
		_, err := db.CreateRace(ctx, model.Race{
			SeasonID: season.ID, Number: 1, Name: "R", Kind: kind, Date: time.Now(),
		})
		return err
	}
	if err := mk(model.RacePoints); err != nil {
		t.Fatal(err)
	}
	// A championship may reuse number 1; a second points race 1 may not.
	if err := mk(model.RaceChampionship); err != nil {
		t.Fatalf("championship #1 should be allowed alongside points #1: %v", err)
	}
	if err := mk(model.RacePoints); err == nil {
		t.Error("duplicate points race number should be rejected")
	}
}

func TestSettings(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if got, _ := db.Setting(ctx, KeyDerbySitePath, "fallback"); got != "fallback" {
		t.Errorf("unset Setting = %q, want fallback", got)
	}
	if err := db.SetSetting(ctx, KeyDerbySitePath, "/tmp/site"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Setting(ctx, KeyDerbySitePath, "fallback"); got != "/tmp/site" {
		t.Errorf("Setting = %q", got)
	}
	// Upsert, not a duplicate row.
	if err := db.SetSetting(ctx, KeyDerbySitePath, "/tmp/other"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Setting(ctx, KeyDerbySitePath, ""); got != "/tmp/other" {
		t.Errorf("after update Setting = %q", got)
	}

	db.SetSetting(ctx, KeyAutoAdvanceSecs, "12")
	if got, _ := db.SettingInt(ctx, KeyAutoAdvanceSecs, 8); got != 12 {
		t.Errorf("SettingInt = %d, want 12", got)
	}
	// A corrupt value must fall back rather than fail a race night.
	db.SetSetting(ctx, KeyAutoAdvanceSecs, "not-a-number")
	if got, _ := db.SettingInt(ctx, KeyAutoAdvanceSecs, 8); got != 8 {
		t.Errorf("SettingInt on garbage = %d, want the default 8", got)
	}

	db.SetSetting(ctx, KeyReverseLanes, "1")
	if got, _ := db.SettingBool(ctx, KeyReverseLanes, false); !got {
		t.Error("SettingBool should read \"1\" as true")
	}
}

func TestAudit(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if err := db.Audit(ctx, "coordinator", "preflight.override", "gate switch jammed"); err != nil {
		t.Fatal(err)
	}
	entries, err := db.RecentAudit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Action != "preflight.override" || entries[0].Detail != "gate switch jammed" {
		t.Errorf("got %+v", entries[0])
	}
	if entries[0].At.IsZero() {
		t.Error("audit timestamp did not round-trip")
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	wantErr := errors.New("boom")
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO setting (key, value) VALUES ('doomed', '1')`); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Tx err = %v, want %v", err, wantErr)
	}
	if got, _ := db.Setting(ctx, "doomed", ""); got != "" {
		t.Errorf("rolled-back write is still visible: %q", got)
	}
}

func TestTxCommitsOnSuccess(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	err := db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO setting (key, value) VALUES ('kept', 'yes')`)
		return err
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}
	if got, _ := db.Setting(ctx, "kept", ""); got != "yes" {
		t.Errorf("committed write missing, got %q", got)
	}
}
