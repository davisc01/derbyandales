// Package model holds the domain types shared across the application.
//
// These mirror the SQLite schema in internal/store/migrations. Timestamps are
// stored as Unix seconds in the database and converted at the store boundary,
// so everything above the store deals in time.Time.
package model

import "time"

// Season is one racing year. The championship's shape is derived entirely from
// RaceCount, AutoQualPlaces and WildcardSpots, so changing the number of races
// in a season needs no code change.
type Season struct {
	ID            int64
	Year          int
	Name          string
	LaneCount     int
	TrackLengthFt float64
	ScaleDenom    int

	RaceCount            int
	AutoQualPlaces       int
	WildcardSpots        int
	MaxChampionshipEntry int
	PointsCountControl   bool
	BracketLaneA         int
	BracketLaneB         int

	CreatedAt time.Time
}

// RaceKind distinguishes a points-scoring season race from the championship.
type RaceKind string

const (
	RacePoints       RaceKind = "points"
	RaceChampionship RaceKind = "championship"
)

// RaceStatus tracks a race through the run-of-show.
type RaceStatus string

const (
	StatusSetup    RaceStatus = "setup"
	StatusCheckin  RaceStatus = "checkin"
	StatusRacing   RaceStatus = "racing"
	StatusVoting   RaceStatus = "voting"
	StatusComplete RaceStatus = "complete"
)

// Race is one event night.
type Race struct {
	ID        int64
	SeasonID  int64
	Number    int
	Name      string
	Date      time.Time
	Venue     string
	Kind      RaceKind
	Status    RaceStatus
	CreatedAt time.Time
}

// Racer is a person, identified by a row rather than by name string. The old
// tracker keyed season points on the name text, so a typo silently split a
// racer's season in two.
type Racer struct {
	ID        int64
	SeasonID  int64
	FirstName string
	LastName  string
}

// FullName renders the racer the way standings.csv expects it.
func (r Racer) FullName() string {
	if r.FirstName == "" {
		return r.LastName
	}
	if r.LastName == "" {
		return r.FirstName
	}
	return r.FirstName + " " + r.LastName
}

// Entry is one car in one race. A racer may have several.
type Entry struct {
	ID          int64
	RaceID      int64
	RacerID     int64
	CarNumber   int
	CarName     string
	PhotoID     *int64
	IsControl   bool
	Excluded    bool
	CheckedInAt *time.Time
	Note        string
}

// HeatPhase separates the qualifying schedule from bracket matchups.
type HeatPhase string

const (
	PhaseQualifying HeatPhase = "qualifying"
	PhaseBracket    HeatPhase = "bracket"
)

// HeatStatus tracks a single heat.
type HeatStatus string

const (
	HeatPending  HeatStatus = "pending"
	HeatArmed    HeatStatus = "armed"
	HeatRunning  HeatStatus = "running"
	HeatComplete HeatStatus = "complete"
)

// Heat is one trip down the track for up to LaneCount cars.
type Heat struct {
	ID               int64
	RaceID           int64
	Number           int
	Phase            HeatPhase
	BracketMatchupID *int64
	Status           HeatStatus
	ArmedAt          *time.Time
	CompletedAt      *time.Time
	Lanes            []HeatLane
}

// HeatLane is one car's run in one lane of one heat. A nil EntryID is a bye:
// the lane runs empty and the timer masks it.
//
// Schedule and result live in the same row, so "FinishTime IS NULL" is the
// universal "not yet run" predicate.
type HeatLane struct {
	HeatID      int64
	Lane        int
	EntryID     *int64
	FinishTime  *float64
	FinishPlace *int
	Ignored     bool
}

// Run reports whether this lane has a usable result.
func (l HeatLane) Run() bool {
	return l.EntryID != nil && l.FinishTime != nil && !l.Ignored
}

// AwardSource records how an award winner was decided.
type AwardSource string

const (
	AwardAuto   AwardSource = "auto"   // derived from finishing order
	AwardVote   AwardSource = "vote"   // declared from a ballot tally
	AwardManual AwardSource = "manual" // entered by the coordinator
)

// Award is one trophy for one race.
type Award struct {
	ID        int64
	RaceID    int64
	Name      string
	AwardType string
	EntryID   *int64
	Sort      int
	Source    AwardSource
}

// VoteCategory is one ballot question for one race, e.g. Best Theme.
type VoteCategory struct {
	ID            int64
	RaceID        int64
	Key           string
	Label         string
	Enabled       bool
	WinnerEntryID *int64
}

// Vote is a single tap on the voting tablet. Storing votes as rows rather than
// as a counter on the car is what makes undo, void and audit possible.
type Vote struct {
	ID         int64
	CategoryID int64
	EntryID    int64
	CastAt     time.Time
	Voided     bool
}

// SeedOrigin says where a championship entrant came from. It is deliberately
// separate from whether they hold a bye: a bye racer is still an auto-qualifier,
// which the old seed-range model could not express.
type SeedOrigin string

const (
	OriginQualifier SeedOrigin = "qualifier"
	OriginWildcard  SeedOrigin = "wildcard"
)

// BracketSeed places one entry in the championship field.
type BracketSeed struct {
	RaceID  int64
	EntryID int64
	Seed    int
	Origin  SeedOrigin
}

// BracketMatchup is one head-to-head pairing. Round is 1-based; Position is the
// vertical slot within the round.
type BracketMatchup struct {
	ID            int64
	RaceID        int64
	Round         int
	Position      int
	TopEntryID    *int64
	BottomEntryID *int64
	WinnerEntryID *int64
	HeatID        *int64
}

// Walkover reports whether this matchup has only one competitor, in which case
// that competitor advances without racing.
func (m BracketMatchup) Walkover() bool {
	return (m.TopEntryID == nil) != (m.BottomEntryID == nil)
}

// Adjustment is a manual correction to a racer's wildcard points.
type Adjustment struct {
	ID        int64
	SeasonID  int64
	RacerID   int64
	Points    int
	Reason    string
	CreatedAt time.Time
}

// QualifierSubstitution replaces an over-limit auto-qualifier slot with the
// next eligible finisher from that race.
type QualifierSubstitution struct {
	SeasonID          int64
	OriginalEntryID   int64
	SubstituteEntryID int64
	CreatedAt         time.Time
}

// Display is a connected screen. Devices self-register, so there are no IP
// addresses to configure.
type Display struct {
	ID         int64
	Name       string
	Token      string
	Page       string
	Params     string
	LastSeenAt time.Time
}

// Photo is a car photo, stored on disk and addressed by content hash.
type Photo struct {
	ID        int64
	SHA256    string
	Ext       string
	Width     int
	Height    int
	CreatedAt time.Time
}

// AuditEntry records a decision worth being able to explain later — most
// importantly a coordinator overriding a failed check.
type AuditEntry struct {
	ID     int64
	At     time.Time
	Actor  string
	Action string
	Detail string
}
