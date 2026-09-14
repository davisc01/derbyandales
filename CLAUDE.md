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
in unit tests.

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

- **Racing pauses for an intermission halfway through the heats**, and that is
  when people vote for the design and theme trophies. Voting opens when the
  intermission starts and closes when it ends. It has **no set length** — the
  venue is a bar, people are refuelling, and it ends when the coordinator says
  so. Never show a countdown. Not built yet; see `docs/ROADMAP.md`.
- **Championship field size is derived**, never hardcoded:
  `entrants = races × auto_qual_places + wildcards`, `capacity = next power of 2`,
  `byes = capacity − entrants`. The club's 24/32/8/5-rounds falls out of that.

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
- **Float ties.** DerbyNet computes drop-slowest as `(SUM−MAX)/(COUNT−1)`, which
  can land one bit away from summing the kept times. `scoring.equalTimes` uses an
  epsilon so two cars that ran identical times tie regardless of arithmetic order.

## Layout

```
internal/
  app/        lifecycle, TLS, backups, preflight, timer controller, race controller, photos
  bus/        in-process event bus, fanned out over SSE
  model/      domain types
  schedule/   heat generation (offset search) and running order
  scoring/    drop-slowest averaging, placement, scale MPH, CSV formatting
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
