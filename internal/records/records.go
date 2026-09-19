// Package records keeps the club's records: fastest run, fastest average,
// fastest in each lane, and everybody's personal best — across every season on
// file, the archive included.
//
// Records are derived, never stored, for the same reason the speed trophies
// are. A stored record could only ever disagree with the times: a heat re-run,
// a lane struck out, a time corrected the morning after. So is "broken": a
// record was broken when a run beat everything that came before it, which the
// times can answer at any point.
//
// Only runs and results that count are passed in. The pace car is equipment,
// an excluded car was ruled out, and a non-finish is not a time — the caller
// leaves all of those out, so nothing here has to know about them.
package records

import (
	"fmt"
	"sort"
	"strings"
)

// epsilon is how close two times must be to be the same. Times are recorded to
// the thousandth, but an average is a division, and tying a record is not
// breaking it.
const epsilon = 1e-9

// Race identifies a race night, and puts them in order.
type Race struct {
	Year         int
	Championship bool
	Number       int
}

// Before reports whether r was raced before o. The championship closes its
// season.
func (r Race) Before(o Race) bool {
	if r.Year != o.Year {
		return r.Year < o.Year
	}
	if r.Championship != o.Championship {
		return !r.Championship
	}
	return r.Number < o.Number
}

// Label is the race as the club's site names it.
func (r Race) Label() string {
	if r.Championship {
		return fmt.Sprintf("%d Championship", r.Year)
	}
	return fmt.Sprintf("%d Race %d", r.Year, r.Number)
}

// Car is who ran, as it is shown.
type Car struct {
	// Person is the racer's identity across years; see history.PersonKey.
	Person    string
	Driver    string
	CarName   string
	CarNumber int
}

func (c Car) key() string {
	return c.Person + "\x00" + strings.ToLower(strings.TrimSpace(c.CarName))
}

// Run is one counted trip down one lane.
type Run struct {
	Car
	Race Race
	// Seq orders the heats within a race, and runs sharing it ran together.
	Seq  int
	Lane int
	Time float64
	// RunOff marks a run in a heat that settled a tie. It is a real run, so it
	// can set a record, but it is not part of the night as scheduled.
	RunOff bool
}

func (r Run) before(race Race, seq int) bool {
	if r.Race != race {
		return r.Race.Before(race)
	}
	return r.Seq < seq
}

// Result is one counted car's finish in a race.
type Result struct {
	Car
	Race  Race
	Place int
	// Average is the drop-slowest average, or zero where there is none to
	// compare — a bracket is decided head to head, not by average.
	Average float64
}

// Book is every record, worked out from the runs and results given.
type Book struct {
	// FastestRun is the current record, and RunHistory every run that set it in
	// turn, oldest first — so the last entry is the holder.
	FastestRun *Run
	RunHistory []Run

	FastestAverage *Result
	AverageHistory []Result

	// Lanes are the fastest run in each lane, lane 1 first.
	Lanes []Run

	// TopRuns and TopAverages are the leaderboards, one entry per car so a
	// single quick car does not fill the table.
	TopRuns     []Run
	TopAverages []Result

	Career []Career

	// PersonalBests is everybody's fastest run, fastest first.
	PersonalBests []Run

	// Lookalikes are names that may be one person spelled two ways. Each
	// splits somebody's career in two; the fix is to correct the spelling at
	// the source, which a person has to decide.
	Lookalikes [][2]string
}

// Career is one racer's tally across every season on file.
type Career struct {
	Person  string
	Driver  string
	Wins    int // points races won
	Podiums int // points races finished in the top three
	Cups    int // championships won
	Nights  int // race nights with a car in the results
}

// Leaderboard length.
const topN = 10

// Compute works out every record.
func Compute(runs []Run, results []Result) Book {
	var b Book

	runs = append([]Run(nil), runs...)
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].Race != runs[j].Race {
			return runs[i].Race.Before(runs[j].Race)
		}
		if runs[i].Seq != runs[j].Seq {
			return runs[i].Seq < runs[j].Seq
		}
		return runs[i].Time < runs[j].Time
	})

	// The record's history is walked in the order it happened. Within one heat
	// the runs are sorted fastest first, so two cars under the old record in
	// the same heat do not both appear to have set it.
	lanes := map[int]Run{}
	pbs := map[string]Run{}
	for _, r := range runs {
		if b.FastestRun == nil || r.Time < b.FastestRun.Time-epsilon {
			b.RunHistory = append(b.RunHistory, r)
			b.FastestRun = &b.RunHistory[len(b.RunHistory)-1]
		}
		if cur, ok := lanes[r.Lane]; !ok || r.Time < cur.Time-epsilon {
			lanes[r.Lane] = r
		}
		if cur, ok := pbs[r.Person]; !ok || r.Time < cur.Time-epsilon {
			pbs[r.Person] = r
		}
	}
	if b.FastestRun != nil {
		held := b.RunHistory[len(b.RunHistory)-1]
		b.FastestRun = &held
	}
	for _, r := range lanes {
		b.Lanes = append(b.Lanes, r)
	}
	sort.Slice(b.Lanes, func(i, j int) bool { return b.Lanes[i].Lane < b.Lanes[j].Lane })
	for _, r := range pbs {
		b.PersonalBests = append(b.PersonalBests, r)
	}
	sortRuns(b.PersonalBests)
	b.TopRuns = bestRunPerCar(runs)

	results = append([]Result(nil), results...)
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Race != results[j].Race {
			return results[i].Race.Before(results[j].Race)
		}
		return results[i].Average < results[j].Average
	})
	for _, r := range results {
		if r.Average <= 0 {
			continue
		}
		if b.FastestAverage == nil || r.Average < b.FastestAverage.Average-epsilon {
			b.AverageHistory = append(b.AverageHistory, r)
			held := r
			b.FastestAverage = &held
		}
	}
	b.TopAverages = bestAveragePerCar(results)
	b.Career = career(results)
	b.Lookalikes = lookalikes(runs, results)
	return b
}

// sortRuns orders runs fastest first, the earlier run ahead on a tie: the
// first to run a time holds it.
func sortRuns(runs []Run) {
	sort.SliceStable(runs, func(i, j int) bool {
		if d := runs[i].Time - runs[j].Time; d < -epsilon || d > epsilon {
			return d < 0
		}
		return runs[i].before(runs[j].Race, runs[j].Seq)
	})
}

func bestRunPerCar(runs []Run) []Run {
	best := map[string]Run{}
	for _, r := range runs {
		if cur, ok := best[r.key()]; !ok || r.Time < cur.Time-epsilon {
			best[r.key()] = r
		}
	}
	out := make([]Run, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	sortRuns(out)
	if len(out) > topN {
		out = out[:topN]
	}
	return out
}

func bestAveragePerCar(results []Result) []Result {
	best := map[string]Result{}
	for _, r := range results {
		if r.Average <= 0 {
			continue
		}
		if cur, ok := best[r.key()]; !ok || r.Average < cur.Average-epsilon {
			best[r.key()] = r
		}
	}
	out := make([]Result, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if d := out[i].Average - out[j].Average; d < -epsilon || d > epsilon {
			return d < 0
		}
		return out[i].Race.Before(out[j].Race)
	})
	if len(out) > topN {
		out = out[:topN]
	}
	return out
}

func career(results []Result) []Career {
	by := map[string]*Career{}
	nights := map[string]map[Race]bool{}
	for _, r := range results {
		c := by[r.Person]
		if c == nil {
			c = &Career{Person: r.Person}
			by[r.Person] = c
			nights[r.Person] = map[Race]bool{}
		}
		// The most recent spelling is the one shown.
		c.Driver = r.Driver
		nights[r.Person][r.Race] = true
		switch {
		case r.Race.Championship && r.Place == 1:
			c.Cups++
		case !r.Race.Championship && r.Place == 1:
			c.Wins++
		}
		if !r.Race.Championship && r.Place >= 1 && r.Place <= 3 {
			c.Podiums++
		}
	}
	out := make([]Career, 0, len(by))
	for p, c := range by {
		c.Nights = len(nights[p])
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Wins != b.Wins {
			return a.Wins > b.Wins
		}
		if a.Podiums != b.Podiums {
			return a.Podiums > b.Podiums
		}
		if a.Cups != b.Cups {
			return a.Cups > b.Cups
		}
		return a.Driver < b.Driver
	})
	return out
}

// Notice is a record broken by one run or one result.
type Notice struct {
	Kind NoticeKind
	// Previous is what it beat.
	Previous Run
	// PreviousAverage is set instead for an average record.
	PreviousAverage Result
}

// NoticeKind says which record fell. They are listed biggest first, and a run
// that breaks several is announced as the biggest.
type NoticeKind string

const (
	TrackRecord   NoticeKind = "track"
	LaneRecord    NoticeKind = "lane"
	PersonalBest  NoticeKind = "pb"
	AverageRecord NoticeKind = "average"
)

// HeatNotices reports which runs in one heat broke a record, keyed by lane.
//
// "Broke" means beat everything that ran before this heat. There has to be
// something to beat: with no history on file, the first heat of the night
// would otherwise set a track record in every lane.
//
// A personal best is only announced for somebody who has raced on an earlier
// night. On a first night every improvement is a personal best, and a screen
// saying so half the heats running is noise.
func HeatNotices(runs []Run, race Race, seq int) map[int]Notice {
	var (
		track    *Run
		lane     = map[int]Run{}
		personal = map[string]Run{}
		veteran  = map[string]bool{}
		heat     []Run
	)
	for _, r := range runs {
		switch {
		case r.Race == race && r.Seq == seq:
			heat = append(heat, r)
			continue
		case !r.before(race, seq):
			continue
		}
		if track == nil || r.Time < track.Time-epsilon {
			held := r
			track = &held
		}
		if cur, ok := lane[r.Lane]; !ok || r.Time < cur.Time-epsilon {
			lane[r.Lane] = r
		}
		if cur, ok := personal[r.Person]; !ok || r.Time < cur.Time-epsilon {
			personal[r.Person] = r
		}
		if r.Race != race {
			veteran[r.Person] = true
		}
	}

	// Only the fastest in the heat can take the track record, and only a
	// racer's fastest car in it their personal best: two cars under the old
	// mark in one heat did not both set it.
	fastest := func(keep func(Run) bool) float64 {
		best := 0.0
		for _, r := range heat {
			if keep(r) && (best == 0 || r.Time < best) {
				best = r.Time
			}
		}
		return best
	}
	heatBest := fastest(func(Run) bool { return true })

	out := map[int]Notice{}
	for _, r := range heat {
		switch {
		case track != nil && r.Time < track.Time-epsilon && r.Time <= heatBest+epsilon:
			out[r.Lane] = Notice{Kind: TrackRecord, Previous: *track}
		case hasFaster(lane, r.Lane, r.Time):
			out[r.Lane] = Notice{Kind: LaneRecord, Previous: lane[r.Lane]}
		case veteran[r.Person] && hasFasterPerson(personal, r.Person, r.Time) &&
			r.Time <= fastest(func(o Run) bool { return o.Person == r.Person })+epsilon:
			out[r.Lane] = Notice{Kind: PersonalBest, Previous: personal[r.Person]}
		}
	}
	return out
}

func hasFaster(lane map[int]Run, l int, t float64) bool {
	cur, ok := lane[l]
	return ok && t < cur.Time-epsilon
}

func hasFasterPerson(p map[string]Run, person string, t float64) bool {
	cur, ok := p[person]
	return ok && t < cur.Time-epsilon
}

// AverageNotices reports which cars in a race ran an average faster than any
// earlier race's, keyed by car number.
//
// Every car that beat the old record is reported, not only the fastest. The
// reveal runs slowest first, so the slower of two record-breakers really does
// hold the record for a minute before the faster one takes it from them.
func AverageNotices(results []Result, race Race) map[int]Notice {
	var prev *Result
	for _, r := range results {
		if r.Average <= 0 || !r.Race.Before(race) {
			continue
		}
		if prev == nil || r.Average < prev.Average-epsilon {
			held := r
			prev = &held
		}
	}
	out := map[int]Notice{}
	if prev == nil {
		return out
	}
	for _, r := range results {
		if r.Race == race && r.Average > 0 && r.Average < prev.Average-epsilon {
			out[r.CarNumber] = Notice{Kind: AverageRecord, PreviousAverage: *prev}
		}
	}
	return out
}
