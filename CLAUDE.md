# Working on Derby and Ales

Race software for MidSouth Derby and Ales, an adult pinewood derby league.
One macOS app runs the whole season. See [README.md](README.md) for what it is
and [docs/ROADMAP.md](docs/ROADMAP.md) for what is left to build.

## Commands

```sh
make check                      # gofmt, vet, tests — run before committing
make test                       # tests only
go test ./... -race             # the concurrency-sensitive packages need this
make app                        # dist/DerbyAndAles.app (universal binary)
go run ./cmd/derbyandales -demo # a practice season, no hardware needed
go run ./cmd/derbyandales -demo-championship -data /tmp/x  # championship night
```

`-demo` plus the simulated timer means a whole race night can be rehearsed on a
laptop. Use it — most bugs in this codebase have surfaced that way rather than
in unit tests. It seeds five nights already raced and scored plus a sixth at
check-in, so the season and championship screens have real data on them; the
fabricated results deliberately include a racer who reaches the cap — so a place
passes down — and an ineligible car, because those paths are otherwise never
seen. Demo racers build a new car ("Lightning Bug II") once one qualifies.

## The club's rules

These are the domain, and getting them wrong changes published results.

- **6 races a season, plus a single-elimination championship.** 4 lanes, 28 ft
  track, cars at 1:25 scale.
- **Every car runs 4 times, once in each lane.** Drop the slowest run, average
  the rest, fastest average wins.
- **Scale MPH** = `track_ft / time × 3600 × 25 ÷ 5280`. With 28 ft, a 2.434 s
  run is 196.1 mph. This is asserted against the club's published results in
  `internal/scoring/golden_test.go`.
- **Three kinds of entry**, and they differ:

  | | Races? | In standings? | Award / points / qualify? |
  |---|---|---|---|
  | Normal | yes | yes | yes |
  | CONTROL pace car | yes | **yes** | no |
  | Excluded (ineligible) | yes | **no** | no |

  Use `Entry.Scores()` and `Entry.EarnsPoints()` rather than testing the flags.
  In 2026 race 4 the excluded car had the fastest average of the night, so this
  decided the winner of record.

  The pace car is **equipment, not a person**. It must not be counted in the
  field size, and its "driver" must never appear in a list of racers — both
  happened in the old tracker, which is why "Derby Ales" is ranked 36th in the
  club's published 2026 wildcard standings. `store.CompetingRacers` is the list
  to offer anywhere a human is being chosen.

- **A heat where every car ran its slowest — or every car its fastest — is a
  fault, not a result.** Once every heat is run, `scoring.HeatAnomalies` flags
  those and the coordinator can re-run them. The `Clear` field is the one to act
  on: it also requires each car to be further off its own form than its other
  runs are from each other, which is what keeps the check quiet on a clean night
  (about one clean race in four hundred, against one in five for the bare rule).
  Do not replace that with a threshold in seconds — a fixed threshold gets
  noisier as the field gets scrappier, and this does not.
- **Racing pauses for an intermission halfway through the heats**, and that is
  when people vote for the design and theme trophies. Voting opens when the
  intermission starts and closes when it ends. It has **no set length** — the
  venue is a bar, people are refuelling, and it ends when the coordinator says
  so. Never show a countdown.
- **Ties share a place and the next distinct place skips.** Two cars tied for
  6th are followed by 8th. That stands everywhere except the podium: **1st, 2nd
  and 3rd are run off**, because a trophy is handed to one person. Below that a
  tie is published as a tie.
- **A run-off's times are not part of any average.** The rule is four runs, one
  per lane, drop the slowest — a fifth run for two cars would rewrite the very
  averages that tied. `heat.runoff_place` marks those heats and every scoring
  query leaves them out. The tied cars keep identical averages afterwards and
  only their places differ.
- **The end of the night runs in this order**: present the two voted trophies
  on their own → reveal the results slowest to fastest, handing over 1st, 2nd
  and 3rd as those cars come up → run off any tie → final standings for the
  wrap-up → publish.

  Each part of that is deliberate. The voted trophies go first because they are
  about how a car looks, and giving them out while the room is thinking about
  speed buries them. The speed trophies are handed over *during* the reveal, so
  the reveal is the ceremony rather than a preamble to one. And the tie is
  settled after the reveal, because the reveal is where the room learns there
  is one.
- **The speed trophies are derived, never stored.** They are the top three of
  the standings, so a stored copy could only ever disagree with it — after a
  re-run, a struck-out lane, a corrected time. `SpeedAwards` computes them;
  `RaceAwards` merges them with the voted and manual ones, and a stored award of
  the same name wins so an override still sticks.
- **The speed trophies are named `1st`, `2nd`, `3rd`.** Seasons up to 2026
  published "Fastest in Event" and so on; the club asked for the short names, so
  files from 2027 differ from the archive in that column.
- **Wildcard points** go to the racer's *best* car only, and only if that best
  place did not already auto-qualify. A racer who wins the night earns nothing
  for their second car finishing 7th either — they already have their slot.
  `points = max(0, field − (place − (auto_qual_places + 1)))`.
- **A finished race is frozen.** Places and points are written to `race_result`
  when the last heat lands and do not move afterwards. Recompute is an explicit,
  audited action that takes a backup first. Never recompute as a side effect of
  a setting change.
- **Championship field size is derived**, never hardcoded:
  `entrants = races × auto_qual_places + wildcards`, `capacity = next power of 2`,
  `byes = capacity − entrants`. The club's 24/32/8/5-rounds falls out of that.
- **Auto-qualifying is decided race by race, in order.** The top
  `auto_qual_places` cars of a race take a championship place each, with two
  exceptions that pass the place down to the next car:
  - **A car that has qualified may not race again** before the championship.
    If one does, it cannot take a second place. Check-in warns when a car that
    already qualified is entered, and offers to mark it ineligible.
  - **A racer at the cap** (`max_championship_entry`, 3) still races, but a top
    finish gives them nothing: the place goes to the next car. They are also
    out of wildcard contention.

  So nobody is ever over the cap and nothing is substituted by hand. The club's
  2026 file shows the old manual version of this — Chris Bryan on four places
  with Greg Thrift listed as a `9*` standby — and `season.Qualifiers` produces
  that standby resolved. A place that passes down onto a tie is run off like a
  tie for a trophy (`TieView.ForPlace`), because only one car can have it.
- **Championship seeding order**: race winners by average time, then the
  remaining auto-qualifiers by average time, then the wildcard racers by season
  points. Seeds 1–8 get a bye.

  Both halves of that are more derived than they look. "Seeds 1–6 are the race
  winners" means *the winners first* — six is the number of races. And seeds 7–8
  are not a separate rule from 9–18: they are one ordered run, and the split at
  8 is where the byes stop. The byes land on the top seeds because that is what
  the bracket construction does, not because anything says so. "Race winner"
  is the best qualifier from each race (`Slot.RaceTop`), which is the winner
  unless their place passed down — the seed passes down with the place.
- **An inherited place still earns wildcard points by finishing place.** A 4th
  who inherits a qualifying place scores 4th-place points for that race; the
  club confirmed it, and it is what the published 2026 standings show.
- **The championship decides one trophy: The D'Ale Cup.** No intermission, no
  third-place matchup, and only a tie for 1st is run off there
  (`store.TrophyPlaces`).
- **A championship is a normal race unless flagged as a bracket.**
  `race.format` is `standard` or `bracket`, and only a championship may be a
  bracket. Every championship from 2023 to 2025 was a normal race, so nothing
  bracket-shaped — building it, arming matchups, the bracket scene, its steps on
  the run-of-show — should appear for a race where `Race.Bracket()` is false.
  No championship of either format has an intermission: there is no vote.
- **A bracket is raced from race control like any other night.** `ArmNext`
  arms the next ready matchup, building its heat then; the times — timed or
  typed in — decide it through `decideMatchup`; racing stops at a champion.
  A dead heat re-runs the same matchup. Re-running a decided matchup takes its
  winner back out of the round above, and is refused once that winner has
  raced again (`UndoMatchupWinner`). `Standings` answers for a bracket by
  round reached — the champion 1st, runner-up 2nd, both semi-final losers
  3rd — so the reveal, the final standings and the published file agree. A
  shared place in a bracket is not a tie to run off and earns no trophy.
- **A bye is not a race.** Walkovers are resolved when the bracket is built, so
  a bye racer appears in round two immediately rather than looking like a
  matchup waiting to happen. A 24-car field is still 23 races: byes move where
  the races happen, not how many there are.

## Conventions

- **Comments explain why, not what.** Nearly every non-obvious line here exists
  because of something that actually goes wrong on a race night. Say what that
  is. A comment restating the code is worse than none.
- **Errors are read by someone in a brewery**, not a developer. "Car number 7 is
  already checked in for this race" — never a SQL constraint name. There is a
  test asserting `UNIQUE` and `idx_` never reach the UI.
- **Report honestly.** The preflight timer check says "Never tested" rather than
  passing. A simulated timer never reports a clean pass. A green tick that means
  nothing is worse than no tick.
- **Never block a race.** Every check that can fail can be overridden with a
  reason, which goes to the audit log. A jammed gate switch must not stop a race
  from happening.
- Tests are named as sentences describing the behaviour, with a comment saying
  why it matters. `TestLaneMappingCatchesReversedWiring`, not `TestLaneMapping2`.

## Traps

Each of these has already caused a bug here.

- **One channel, two consumers** silently loses events. This bit twice — first
  `Identify` racing the read loop, then the app's event pump racing bench
  checks. `Device.Subscribe()` fans out; never read a shared channel from two
  places. The race detector caught one of them; live testing caught the other.
- **Subscribe before acting** in tests, or a fast simulator finishes before the
  listener exists.
- **The FastTrack timer echoes every command back**, and masked lanes still
  report `0.000`. Both are load-bearing.
- **`0.000` means "did not finish"** and is rewritten to `9.999` so it sorts
  last. Left alone it looks like the fastest run of the night.
- **A heat ends on whichever comes first: every lane reporting, or the start
  gate being closed again.** The club's FastTrack sometimes reports nothing at
  all, and waiting for a result that is not coming is not a plan. Resetting the
  gate is what the operator does next anyway, so it is the signal. Any lane the
  timer never mentioned is recorded at **9.999** rather than left out — a lane
  silently missing makes the heat look complete and the car look as though it
  never raced.
- **Do not confuse a bad read with a phantom trigger.** All lanes reading 9.999
  *with the timer having reported every one of them* means it fired with no cars
  on the track, and racing stops for a person. All lanes 9.999 *because nothing
  was reported* is a bad read: the cars did run, it is recorded, and the
  coordinator is told which lanes were silent. `HeatResult.Missing` is what
  separates the two.
- **Gate readings must persist 500 ms** before being believed. Real switches
  bounce, and a bounce would start a race with nothing to time.
- **Camera needs a secure context.** `localhost` or HTTPS only. `isSecureContext`
  in `internal/web/checkin.go` decides whether to offer capture or a file picker.
- **Go templates share a namespace.** Each page is parsed with its own layout
  copy; see `parsePages`. Displays use `layout-bare.html` — a TV must not show a
  nav bar.
- **The intermission is a hard stop.** Auto-advance must not step over it, and
  `ArmNext` *and* `ReRun` both refuse during it. Treating it as a long advance
  delay would let the race restart around people standing at the table. Any new
  way to arm the track needs the same guard — `ReRun` was missed the first time.
- **"Re-run" means re-run.** It is refused on a heat with no times, because
  there it would silently mean "skip ahead to this one". The button is only
  rendered on completed heats.
- **Float ties.** DerbyNet computes drop-slowest as `(SUM−MAX)/(COUNT−1)`, which
  can land one bit away from summing the kept times. `scoring.equalTimes` uses an
  epsilon so two cars that ran identical times tie regardless of arithmetic order.
- **A literal BOM in a Go source file** is a compile error ("illegal byte order
  mark"), and it lands there by writing the character rather than `\ufeff` while
  handling the club's BOM-carrying CSVs. Fix it at byte level; an editor will
  happily rewrite it back.
- **Seeding is two problems.** The *order* comes from the season and is pure.
  *Which car fills each place* cannot be known until people turn up, and is
  matched at championship check-in by racer plus car name. An exact match needs
  no review; anything looser is flagged for a person, because a wrongly-seeded
  car lands in the wrong half and nobody finds out until the semi-final.
- **A race is recorded into the season twice when it has a run-off**: once when
  its last heat lands, and again when the run-off settles it
  (`RaceController.settleRunOff`). The run-off comes after the reveal, so the
  first record still has the tie; without the second, a tie for 3rd would stay
  a tie in the championship places.
- **The published file formats come from the committed files, not from the
  exporters that made them** — the committed ones were hand-trimmed and are what
  the site renders. Asserted in `internal/publish/golden_test.go`. Two traps:
  a **BOM** lives inside the first header cell and silently breaks the season
  page's `hide-columns` and `highlight-column`, and a renamed header does the
  same. `awards.csv` has **no `Award Type` column** — an early note said it did,
  from the one file in five that has one.
- **`index.md` is written once and never again.** It carries a hand-written
  summary and the photo album link. Overwriting it to "keep things in sync"
  would delete the only part of the page a person wrote.
- **The publish diff compares normalised content** (BOM and CRLF stripped) so a
  single corrected time reads as one line rather than a whole-file rewrite. A
  file that differs *only* by a BOM is still rewritten, because the BOM is the
  thing that breaks the site.
- **Exactly one run-of-show step is ever "next".** `markNext` enforces it:
  several steps are genuinely available at once, and the screen exists to answer
  one question. The intermission overrides the order, because it is a hard stop
  rather than a step in a queue.
- **A car gets one championship**, and that is checked against the club's own
  published archive rather than from memory. `internal/history` parses six years
  of championship standings across four different exporters — one year's header
  wraps across lines, one year baked the qualifying origin into every car name
  as a `-1`..`-4`/`-W` suffix, and "Drive-By" proves the stripping has to be
  narrow. Cars are matched by name folded for case and spacing; a surname match
  as well is the strong case. It **flags, never excludes** — excluding a car is
  a decision and it needs a reason.
- **Times can be entered by hand**, and it is the same path a timer's take:
  `RaceController.EnterTimes` derives the places rather than trusting them, and
  a zero becomes 9.999 the way the driver rewrites a timer's. It is how the
  software is demonstrated without hardware and how a bad reading is corrected.
- **The now-racing screen has three phases**: staged, running, result. While the
  gate is open the screen clears — the cars leave to the right — because there
  is nothing to report for two seconds and a frozen table reads as a broken one.
  The results come back in finish order, not lane order. Gold for the heat
  winner, red for a car that did not finish, showing the 9.999 that goes into
  the results.
- **The impound screen shows two heats — on the track, and to load next — as
  lanes, pictures and car numbers only.** The official reading it is loading a
  tray, not watching the racing, so a driver's name is noise. A display can be
  pinned to one scene with `/display?scene=impound`; a pinned screen ignores
  scene changes from the coordinator, which is the point.
- **The club's logo is `internal/web/static/logo.png`** (256 px, from
  `mdna-derbynet/images/mdna-circle-standard.png`) and `packaging/AppIcon.icns`
  is built from the same source. Regenerate both from that file rather than
  editing either.
- **A backup is restored across a restart**, never in place. Every controller
  reads the database through `a.DB`; swapping it under a heat being recorded is
  a data race. `StageRestore` copies the snapshot to `restore-pending.sqlite3`
  (outside `backups/`, where pruning could delete it) and the app stops;
  `Open` swaps it in before the database is opened, and removes the old
  `-wal`/`-shm` so SQLite does not replay them over it.
- **Everything runs offline.** The venue has private wifi and no internet. No
  CDNs, no web fonts, no outbound HTTP: every asset is `go:embed`ed and served
  from the app itself, and the whole page set is verified to reference only
  relative paths. Keep it that way — one `<link>` to a font service would fail
  silently on race night and only there.

## Layout

```
internal/
  app/        lifecycle, TLS, backups, preflight, timer controller, race controller, photos
  bus/        in-process event bus, fanned out over SSE
  model/      domain types
  schedule/   heat generation (offset search) and running order
  bracket/    seed order, bracket construction, the Championship Planner
  history/    reading the club's published archive back in
  publish/    website CSV writers, page skeletons, diff and write
  scoring/    drop-slowest averaging, placement, scale MPH, CSV formatting
  season/     wildcard points, auto-qualifying (pass-down), seeding
  store/      SQLite, migrations, queries
  timer/      FastTrack driver, simulator, state machine, Test Bench
  web/        handlers, templates, static assets
```

Migrations are additive and embedded; add a new numbered file in
`internal/store/migrations/` rather than editing an applied one.

## Related repos — read only

The user has asked that these never be modified.

- `~/git/derbynet` — upstream DerbyNet, the system being replaced.
- `~/git/mdna-derbynet` — the Ansible customizations and MDnATracker
  (Python/Flask, at `mdna-tracker/app.py`).
- `~/git/derby-site` — the Hugo site. **The folder is `derby-site`; its git
  remote is `mdna-site`.** There is no `~/git/mdna-site`.
