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
```

`-demo` plus the simulated timer means a whole race night can be rehearsed on a
laptop. Use it — most bugs in this codebase have surfaced that way rather than
in unit tests. It seeds five nights already raced and scored plus a sixth at
check-in, so the season and championship screens have real data on them; the
fabricated results deliberately include an over-limit racer and an ineligible
car, because those paths are otherwise never seen.

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
- **Championship seeding order**: race winners by average time, then the
  remaining auto-qualifiers by average time, then the wildcard racers by season
  points. Seeds 1–8 get a bye.

  Both halves of that are more derived than they look. "Seeds 1–6 are the race
  winners" means *the winners first* — six is the number of races. And seeds 7–8
  are not a separate rule from 9–18: they are one ordered run, and the split at
  8 is where the byes stop. The byes land on the top seeds because that is what
  the bracket construction does, not because anything says so.
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
- **Over limit is `> cap`; maxed out is `>= cap`.** They are different tests and
  both are needed: a racer sitting exactly on the cap keeps every slot they hold
  but is out of wildcard contention. Getting these the same way round silently
  changes who reaches the championship.
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
  scoring/    drop-slowest averaging, placement, scale MPH, CSV formatting
  season/     wildcard points, auto-qualifier seeding, substitutions
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
