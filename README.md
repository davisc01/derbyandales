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
| M1 | Check-in — seasons, races, racers, entries, photo capture | |
| M2 | Schedule and scoring — generator search, drop-slowest standings, scale MPH | |
| M3 | Timer — FastTrack driver, simulator, Timer Test Bench, race control | |
| M4 | Displays — roster, now-racing, results reveal, slideshow, awards | |
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
