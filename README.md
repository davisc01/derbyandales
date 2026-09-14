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

Under construction, targeting the **2027 season**. The run-of-show is
continuous end to end: create a season, check cars in, close check-in, race
every heat, pause for the intermission, vote, reveal the results — and the
night lands in the season standings by itself. [docs/ROADMAP.md](docs/ROADMAP.md)
has the detail for what is left.

| Milestone | | |
|---|---|---|
| M0 | Skeleton — app bundle, database, HTTP/HTTPS, event bus, settings, preflight, backups | **done** |
| M1 | Check-in — seasons, races, racers, entries, photo capture | **done** |
| M2 | Schedule and scoring — generator search, drop-slowest standings, scale MPH | **done** |
| M3 | Timer — FastTrack driver, simulator, Timer Test Bench | **done** |
| M4 | Displays and race control — roster, now-racing, results reveal | **done** (slideshow, awards, bracket with later milestones) |
| M5 | Voting and intermission — ballot, tallies, undo, winner declaration | **done** |
| M6 | Season — auto-qualifiers, wildcard points, substitutions, adjustments | **done** |
| M7 | Bracket — planner, seeding, generation, advance | **done** |
| M8 | Publishing — website CSV writers, run-of-show screen | **done** |
| M9 | Dress rehearsal on real hardware | |

Working on this? Read [CLAUDE.md](CLAUDE.md) first — it records the club's
rules, the conventions, and the traps that have already caused bugs here.

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

That creates a clearly-labelled demo season with **five nights already raced
and scored** and a sixth waiting at check-in, so the season and championship
screens have something real on them from the first launch. The Timer page offers
a simulated timer with buttons that stand in for the person at the track, so the
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

Snapshots are taken at startup, when a race opens, when check-in closes, at the
intermission, when a race completes, and before a season's points are recomputed
— via SQLite's `VACUUM INTO`, which never blocks a write, so a backup can't stall
a heat.

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
  season/           wildcard points, auto-qualifiers, substitutions
  store/            SQLite, migrations, queries
  timer/            FastTrack driver, simulator, state machine
  web/              HTTP handlers, templates, static assets
packaging/          .app bundle build
```

## Design notes

**One database, no CSV round-trip.** Season points read race results directly.
The separate tracker app existed only because DerbyNet and it had different
databases.

**A finished race is frozen.** When the last heat lands, the night's places and
the wildcard points that follow from them are written down and stop moving.
Correcting a result or changing a setting afterwards does not silently rewrite
history — recomputing is an action somebody takes, it is audited, and it takes a
backup first.

**The pace car is equipment, not a competitor.** It races and it is ranked, but
it takes no trophy, earns no points, and is not counted in the field size. The
old tracker counted it, which added a point to everyone's total and put "Derby
Ales" in the published standings as though it were a person.

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

**One screen tells you how to run the night.** The run-of-show is a list, in
order, that says what is done, what is next, and what is stopping it. Nothing on
it is ticked off by being pressed — a step is done when the thing it describes
has actually happened — and exactly one step is ever marked as next, because
three answers to "what do I do now" is the same as none. This is the answer to
the bus-factor problem, and the real acceptance test for the project is handing
it to somebody who has never run a race.

**Publishing writes files and stops.** It never runs git, never commits and
never pushes: it shows a diff of every file it would write, you press the button,
and then you review and commit in the website folder yourself. The five file
formats are asserted against the club's own committed results, and `index.md` is
written once and never again — it carries the summary and the photo album link,
which no export knows about.

**It runs with no internet at all.** The venue is a brewery with a private
wifi network and no uplink. Every asset is compiled into the binary and served
from it — no CDNs, no web fonts, no outbound requests of any kind. Started up,
the process holds two listening sockets and opens nothing.

**A car gets one championship, and the software remembers which.** The club's
own published championship results — six years of them — are read back out of
the website folder, and a car that matches one is flagged at the check-in table.
It flags and stops there: excluding a car is a decision, and it needs a reason.
This was checked from memory before, which worked right up until the person who
remembered was not there.

**The end of the night is a ceremony, and the software knows the order.** The
two voted trophies first, on their own, with the car on the screen. Then the
results slowest to fastest, with 1st, 2nd and 3rd handed over as those cars come
up — the screen names the trophy due. Then any tie is run off, and the settled
table goes up for the wrap-up.

**A tie for a trophy is settled on the track.** Below the top three a tie simply
stands: two cars that ran the same average are the same speed, and the results
say so. But 1st, 2nd and 3rd are handed to a person, so those are run off head
to head — after the reveal, so the room finds out there is a tie the same way it
finds out everything else. The run-off decides the order and nothing else: its
times stay out of every average, so the two cars keep the identical averages
that tied them.

**A heat that went wrong gets found.** Cars are slow for their own reasons, but
if *every* car in one heat ran its worst time of the night, the heat was the
problem: a sticky gate, a knock to the track. The reverse — every car its best —
means the start released early or the timer started late. At the end of the
heats those are listed, with how far outside their own form each car was, and a
button to re-run the heat. Any heat can be re-run at any point: clearing its
times makes it the next one waiting, and the race carries on afterwards from
wherever it had got to.

**Racing pauses itself for the intermission.** Halfway through the heats the
race stops, voting opens, and a backup is taken — all without the coordinator
remembering any of it. There is no set length: the venue is a bar and people
are refuelling, so it ends when someone presses resume.

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
24-entrant, 8-bye, 5-round bracket falls straight out of that formula — verified
against the bracket the club already races — so the number of races in a season
can change without touching code. The old system hardcoded 24 in two
eight-element tables that raised an index error at any other size. The seeding
is filled in from the season's own results rather than typed in by hand, which
was a CSV export, a hand-edit, an import, and then twenty-four seeds.

## Credits

The heat-scheduling approach, the timer protocol handling, and the
schedule-plus-results table design were all learned from
[DerbyNet](https://github.com/jeffpiazza/derbynet) by Jeff Piazza (MIT licensed).
This is an independent implementation, not a fork, but it owes that project a
great deal.

FastTrack timer commands are Micro Wizard's hardware protocol.
