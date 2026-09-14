# Derby and Ales

Race software for [MidSouth Derby and Ales](https://derbyandales.com) — an adult
pinewood derby league racing a 28 ft, 4-lane track with a FastTrack infrared timer.

One macOS app runs the whole season: check-in, heat scheduling, the timer, TV
displays, voting, awards, season points, the championship bracket, and publishing
results to the club website.

## What it replaces

Today a race night spans three systems — a Raspberry Pi running DerbyNet with an
Ansible-applied patch set, a separate Python app for season points, and a CSV
round-trip through `~/Downloads` to get results onto the Hugo site. It works, but
only one person can run it, and the customizations are patches against a moving
upstream.

This is one app, with no runtime for anyone to install, that knows the club's
rules natively.

## Status

Under construction, targeting the **2027 season**.

| Milestone | | |
|---|---|---|
| M0 | Skeleton — app bundle, database, HTTP/HTTPS, event bus, settings, preflight, backups | **done** |
| M1 | Check-in — seasons, races, racers, entries, photo capture | **done** |
| M2 | Schedule and scoring — generator search, drop-slowest standings, scale MPH | **done** |
| M3 | Timer — FastTrack driver, simulator, Timer Test Bench | **done** |
| M4 | Displays and race control — roster, now-racing, results reveal | **done** (slideshow, awards, bracket with later milestones) |
| M5 | Voting — ballot, tallies, undo, winner declaration | |
| M6 | Season — auto-qualifiers, wildcard points, substitutions | |
| M7 | Bracket — planner, seeding, generation, advance | |
| M8 | Publishing — website CSV writers, run-of-show screen | |
| M9 | Dress rehearsal on real hardware | |

## Running it

```sh
make run          # against a scratch data directory, with debug logging
make test         # full suite
make check        # formatting, vet and tests — what CI runs
make app          # build dist/DerbyAndAles.app (universal binary)
```

To try it without a timer or any real data:

```sh
go run ./cmd/derbyandales -demo
```

That creates a clearly-labelled demo season, and the Timer page offers a
simulated timer with buttons that stand in for the person at the track. The
whole race night can be rehearsed on a laptop.

The app starts a web server and opens a browser. Everything happens there.

```
  Coordinator:  http://localhost:8080        camera works
  Check-in:     https://race-mac.local:8443  camera works after trusting the certificate
  Displays:     http://race-mac.local:8080   voting and screens; no camera
```

### Why two ports

Browsers only expose the camera on a *secure context*. `localhost` qualifies, so
check-in on the server Mac works over plain HTTP — but a check-in iPad at
`http://192.168.1.42:8080` does not, and would silently have no camera. So the
app also mints a self-signed certificate covering localhost, the machine's
`.local` name and its current LAN addresses. Trust it once per device and the
camera works there too.

Displays and the voting tablet do not need a camera, so plain HTTP is fine
for them.

## Where data lives

Everything is under one folder, so backing up the club's data is a folder copy:

```
~/Library/Application Support/DerbyAndAles/
  derby.sqlite3     the database
  photos/           car photos, addressed by content hash
  backups/          automatic snapshots
  certs/            the self-signed TLS certificate
  traces/           recorded timer serial sessions
```

Snapshots are taken at startup, when a race opens, when check-in closes, and
when a race completes, via SQLite's `VACUUM INTO` — which never blocks a write,
so a backup can't stall a heat.

## Layout

```
cmd/derbyandales/   entry point
internal/
  app/              lifecycle, paths, TLS, backups, preflight
  bus/              in-process event bus, fanned out over SSE
  bracket/          seeding, pairing, round advance
  model/            domain types
  publish/          website CSV writers
  schedule/         heat generation and ordering
  scoring/          drop-slowest averaging, placement, scale MPH
  season/           auto-qualifiers, wildcard points
  store/            SQLite, migrations, queries
  timer/            FastTrack driver, simulator, state machine
  web/              HTTP handlers, templates, static assets
packaging/          .app bundle build
```

## Design notes

**One database, no CSV round-trip.** Season points read race results directly.
The separate tracker app existed only because DerbyNet and it had different
databases.

**Server-sent events, not polling.** DerbyNet runs three pollers (a 500 ms timer
heartbeat, a 500 ms content poll, a 5 s kiosk poll). Here every browser holds one
SSE connection and updates the instant a result lands.

**Schedule and results share a table.** `heat_lane` holds both, so
`finish_time IS NULL` is the universal "not yet run" predicate. This idea is
borrowed from DerbyNet's `RaceChart` and it simplifies nearly everything
downstream.

**Racers are rows, not name strings.** The old tracker keyed season points on the
name text, so a typo or a changed surname silently split a racer's season.

**The timer is described, not coded.** A timer model is a `Profile`: serial
settings, a probe, regular expressions that turn its output into events. Adding
a second model is data rather than code. The one implemented is Micro Wizard's
FastTrack K/Q-series, which is what the club races on.

**There is a simulator that speaks the real protocol.** It echoes commands,
acknowledges with `*`, answers `RV` and `RF`, holds a gate state, and sends a
genuine unterminated result line. A simulator that shortcut to "here are four
times" would never exercise line assembly, detector excision, masking or the
gate debounce — which is exactly where the bugs are. It also means the Timer
Test Bench can be rehearsed with no hardware present.

**Photos are stored once and resized on demand.** Images are addressed by the
hash of their contents, so a retake of an identical frame costs one file and
re-uploading is free. Derived sizes are a cache — deleting the renders folder
costs nothing but time.

**Displays register themselves.** A screen opens the display address, is given
a name, and appears in the coordinator's list. Scene changes arrive over the
event stream and are swapped in place, so a TV never shows a white flash
mid-race. There are no IP addresses to configure — the person setting up the
screens is carrying an HDMI cable, not a laptop.

**The scheduler searches instead of shipping tables.** DerbyNet ships ~1.4 MB of
precomputed generator tables. Searching for the lane offsets at runtime takes
about a millisecond for a 24-car field and removes the tables entirely. For the
club's field sizes it finds a *perfect* schedule — no two cars ever race each
other twice, and nobody runs in back-to-back heats.

**The championship is derived, not hardcoded.** Field size, byes and rounds all
come from `race_count × auto_qual_places + wildcard_spots`. The club's current
24-entrant, 8-bye, 5-round bracket falls straight out of that formula, so the
number of races in a season can change without touching code.

## Credits

The heat-scheduling approach, the timer protocol handling, and the
schedule-plus-results table design were all learned from
[DerbyNet](https://github.com/jeffpiazza/derbynet) by Jeff Piazza (MIT licensed).
This is an independent implementation, not a fork, but it owes that project a
great deal.

FastTrack timer commands are Micro Wizard's hardware protocol.
