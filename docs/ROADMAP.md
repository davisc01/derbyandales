# Roadmap

What is built, what is left, and the details needed to build it.

The specifications below were derived from the club's existing systems and its
published results. They are recorded here rather than only in a planning
document so they cannot be lost.

## Done

| | | |
|---|---|---|
| M0 | Skeleton | app bundle, SQLite + migrations, HTTP/HTTPS, event bus, settings, preflight, backups |
| M1 | Check-in | seasons, races, racers, entries, photo capture and storage |
| M2 | Schedule and scoring | offset search, running order, drop-slowest standings, scale MPH |
| M3 | Timer | FastTrack K/Q driver, simulator, Timer Test Bench |
| M4 | Displays and race control | registration, scene manager, roster / now-racing / results-reveal, auto-advance |
| M5 | Voting and intermission | ballot tablet, tallies, undo, tie-break, winner declaration, automatic halfway pause |
| M6 | Season points and auto-qualifiers | frozen race results, wildcard points, seeded qualifier list, substitutions, adjustments |
| M7 | Championship bracket | planner, generalized construction, seeding from season data, running the matchups |
| M8 | Publishing and the run of show | five website files with a diff preview, speed trophies, the race-night checklist |

## Left to build

<details>
<summary>M5 — Voting and intermission (built; kept for the reasoning)</summary>

### M5 — Voting

Two categories on one shared tablet: **Best Theme**, then **Best Overall
Design**, then a thank-you that returns to the start after about five seconds.

**No dedupe, deliberately.** The club votes on a single staffed tablet, so
per-device dedupe would block every voter after the first. This looks like a bug
if you do not know why. The effort goes into the tally side instead:

- Live tallies over SSE.
- **Undo the last vote** in one tap — a misclick is the common case.
- Void any individual vote; reset a category.
- **Declare winner**, with a tie-break chooser when the top count is shared.
  Declaring writes an `award` row with `source = 'vote'`.

The ballot is a grid of large tiles — car number, photo, car name — drawn from
the **current race's** entries. The old system hardcoded `classid = 1`, so the
tablet showed the wrong cars whenever race 1 was not loaded.

Schema already exists: `vote_category`, `vote`. Votes are rows, not counters,
which is what makes undo and audit possible.

Categories are per-race and toggleable, replacing a broken configuration flag in
the old system that silently did nothing.

#### Intermission

**Racing pauses halfway through the heats.** During the intermission people come
to the table and vote on the tablet for the design and theme trophies. This is
part of the run of the night, not an incidental break, so the software should
run it rather than leave the coordinator to remember.

- A season (or race) setting `intermission_after_heat`, defaulting to half the
  scheduled heats rounded down — heat 10 of 20, heat 12 of 25. Zero disables it.
- When that heat's results land, the race controller **does not arm the next
  heat**. It publishes an intermission event and waits.
- **Voting opens when the intermission starts and closes when it ends.** The
  window is exactly the intermission, so the coordinator never opens or closes
  voting separately — forgetting either is the failure this prevents.
- Take a **backup** on entering the intermission. It is a known-quiet moment
  halfway through the night, which is when a snapshot is cheapest and most
  useful.
- Displays switch to a voting scene — what to vote for, where the tablet is, and
  a live count of votes cast so the coordinator can see it is being used.
- The race screen shows **Intermission** with a prominent *Resume racing*
  button, the vote tallies, and how many heats remain.
- Resuming closes voting, arms the next heat, and carries on as before.

**There is no set length.** The venue is a bar or a brewery and the intermission
is also when people refuel, so it ends when the coordinator says it ends.
Nothing should show a countdown or a remaining time — not the race screen and
certainly not the TV. The only thing that ends an intermission is someone
pressing *Resume racing*.

Consequences worth building deliberately:

- A ballot loaded outside the voting window should say **voting is closed**
  rather than silently accepting taps that go nowhere. The tablet will be
  sitting on the table the whole night.
- Resuming ends voting, so the coordinator should see the tallies **on the same
  screen as the resume button** — not have to go looking for them first.
- Closing must be reversible. Someone will resume racing a minute before the
  last person votes, and reopening should be one button, not a database edit.

This interacts with auto-advance: the pause has to survive it. Treat the
intermission as a hard stop that auto-advance cannot step over, rather than as a
very long advance delay — a coordinator who nudges *Arm next heat* during the
break should get a confirmation, not a silent restart of the race.

</details>

<details>
<summary>M6 — Season points and auto-qualifiers (built; kept for the reasoning)</summary>

### M6 — Season points and auto-qualifiers

No CSV round-trip: this reads race results from the same database. The separate
tracker app existed only because DerbyNet and it had different databases.

**Wildcard points**, preserving the club's rule:

```
points = max(0, total_racers − (place − (auto_qual_places + 1)))
       = 0  if the racer's best place <= auto_qual_places   (already qualified)
       = 0  if not the racer's best-placed car in that race
       = 0  for the CONTROL car
```

4th place scores `total_racers`; each lower place one fewer. Tied places share a
value and the next distinct place skips, which falls out of using the raw place
number.

Note the second line carefully: it is the racer's **best** place that is tested,
not each car's. A racer who wins the race earns nothing for their second car
finishing 7th either. They have their championship slot; the wildcard list
exists to give one to somebody who does not.

> **Two corrections carried forward.** The old tracker excluded the CONTROL car
> from scoring but still counted it in `total_racers`, inflating everyone's
> points by one — contradicting the club's own published rule ("points equal to
> the number of racers at the race"). It also treated the pace car's driver as a
> racer, which is why "Derby Ales" is ranked 36th on zero points in the
> published 2026 standings. Both are fixed; `points_count_control` restores the
> first, for reproducing an old year exactly.

Points are **frozen at race completion** so changing settings later cannot
silently rewrite history. Recompute is an explicit action, audited, and takes a
backup first.

**Auto-qualifiers**: top `auto_qual_places` (3) from each race. Ordered per the
club's published rule — race winners first by average time, then the remaining
top finishers by average time. The list index *is* the seed.

- Per-racer cap of `max_championship_entry` (3). `over_limit` = count **>** cap
  (needs a substitution); `maxed_out` = count **>=** cap (out of wildcard
  contention). The thresholds differ intentionally; label them clearly.
- **Substitution**: replace an over-limit slot with any non-top-3 finisher from
  that race. Both sides unique, undo supported, CONTROL excluded.
- **Adjustments**: signed points with a mandatory reason, individually removable.

#### What building it settled

- **Both lists were reproduced exactly** from the club's published 2026 files.
  `internal/season/golden_test.go` rebuilds the 34-racer wildcard table and the
  15-seed qualifier list from the five race `standings.csv` files, and asserts
  every rank, name, points value, seed, car, race, finish and entry count. The
  only two rows that differ are the two corrections above, and the test says so
  rather than skipping them.
- **The published `qualifiers.csv` carries a hand-added row seeded `9*`.** That
  is a standby for an over-limit racer, shown *alongside* the slot rather than
  replacing it, because mid-season the substitution had not been committed. The
  app records substitutions properly, so it has no such row — something for M8's
  publisher to decide how to render.
- **`Avg Time` is published to 3 dp with trailing zeros trimmed** (`2.39`), not
  the flat 3 dp the old exporter produced. `scoring.FormatAverage` already does
  this.
- **A substitute can themselves be at the cap.** Handing a slot to somebody who
  already holds three immediately recreates the problem. It stays allowed — on a
  thin night they may be the only person left — but the picker marks them.

</details>

<details>
<summary>M7 — Championship bracket (built; kept for the reasoning)</summary>

### M7 — Championship bracket

Fully parameterized. The old system hardcoded 24 entrants in eight-element
tables that raise an index error at any other size.

```
entrants  N = race_count × auto_qual_places + wildcard_spots   (minus no-shows)
capacity  P = smallest power of 2 >= N
byes        = P − N        awarded to the top seeds
R1 matches  = N − P/2
rounds      = log2(P)
```

**Pairing** uses the standard recursive construction:

```
order(1) = [1]
order(2n) = for each s in order(n): emit s, emit (2n + 1 − s)
```

Verified to reproduce the club's current bracket exactly — same eight R1
matchups (`16v17, 9v24, 13v20, 12v21, 14v19, 11v22, 10v23, 15v18`), same bye
seeds 1–8, same half-split (1/4/5/8 vs 2/3/6/7). Only the vertical rendering
order of the bottom half differs, which is cosmetic; the generated diagram is
now the one people will see, so it is consistent with itself.

#### Seeding order, as the club states it

```
Seeds 1–6    the six winners of the regular season races, by average time
Seeds 7–8    the next two fastest auto-qualifiers, by average time
Seeds 9–18   the remaining auto-qualifiers, by average time
Seeds 19–24  the wild-card racers, by total wildcard points
Seeds 1–8 get a bye into Round 2
```

Two clauses there are consequences rather than rules, and the code says so:

- **7–8 and 9–18 are one ordered run.** Both are "auto-qualifiers by average
  time" and they run straight on. The split at 8 is where the byes stop — and
  the byes are derived too, since 24 cars need a 32 bracket.
- **"Seeds 1–6" means "the winners first".** Six is the number of races. A
  five-race season puts winners in 1–5.

Neither changes the ordering M6 already produced against the published
qualifiers list.

#### What building it settled

- **Seeding is two problems.** The order is pure and comes from the season.
  *Which car* fills each place cannot be known until championship check-in, so
  it is matched by racer plus car name: exact matches are silent, a racer who
  brought a different car is flagged, and an absent racer shrinks the field.
- **A no-show shrinks the field rather than leaving a hole**, because a hole
  would act as a bye nobody earned. Seeds renumber and the byes recompute.
- **A 24-car field is 23 races whatever the byes are.** Every car but the
  champion loses once. Byes move where the races happen — eight in round one
  instead of sixteen — not how many there are.
- **The planner suggests only bye-free shapes.** Listing every workable wildcard
  count was 25 rows of arithmetic rather than a suggestion.
- **A dead heat is not settled by software.** There is no second criterion, and
  inventing one would be worse than asking for the matchup to be run again.

</details>

<details>
<summary>M8 — Publishing and the run of show (built; kept for the reasoning)</summary>

### M8 — Publishing

Settings hold the path to `~/git/derby-site`. "Publish" shows a diff, then
writes. **The app never runs git** — the user reviews and commits.

UTF-8 **without BOM**, LF, comma-delimited. Exact headers:

| Path | Header |
|---|---|
| `content/races/{yyyy}/race-{n}/heats.csv` | `Heat,Lane,FirstName,LastName,CarNumber,CarName,FinishTime,Scale MPH,FinishPlace` |
| `content/races/{yyyy}/race-{n}/standings.csv` | `Place,Car Number,Name,Car Name,Heats,Average,Best,Worst` |
| `content/races/{yyyy}/race-{n}/awards.csv` | `Award Name,First Name,Last Name,Car Number,Car Name` |
| `content/races/{yyyy}/season-standings/qualifiers.csv` | `Current Seed,Driver,Car Name,Race,Race Finish,Avg Time,Total Entries,Over Limit` |
| `content/races/{yyyy}/season-standings/wildcard.csv` | `Rank,Name,Total Points` |

#### What building it corrected

- **`awards.csv` has no `Award Type` column.** This roadmap said it did, taken
  from 2026 race 2 — but the other four races that season and every year before
  use the five columns above. The note was wrong; the club's format is not.
- **CRLF, not LF, is what most committed files have**, and one has no trailing
  newline at all. It does not matter — the site's shortcode uses Go's CSV reader,
  which takes either — so the app writes LF and normalises when *comparing*, so
  a corrected time shows as one changed line rather than a whole-file rewrite.
- **The BOM does matter, and now it is known why.** The `csv-table` shortcode
  matches column names by string equality, and the season page uses
  `hide-columns="Over Limit"` and `highlight-column="Over Limit"`. A BOM lives
  inside the first header cell, so a file written with one silently stops both.
- **The championship lives in `championship/`, not `race-{n}/`**, and gets
  `heats.csv` + `standings.csv` only.
- **The speed trophies are now generated** from the standings, skipping the
  CONTROL car. That was the last open question in this document: `EarnsPoints()`
  finally has a caller on the awards side.
- **`index.md` is written once and never again.** It has the summary and the
  photo album link in it.

#### The run of show

One screen, derived entirely from the state of the database and the timer.
Nothing is ticked off by being pressed. It shipped with nine steps; the end of
the night was later split into five, so it is eleven now (see *Since M8*):

```
1. Open the race       2. Test the timer        3. Check the cars in
4. Introduce           5. Race                  6. Intermission and voting
7. Design & theme      8. Reveal (1st/2nd/3rd)  9. Run off any tie
10. Final standings    11. Publish
```

- **Exactly one step is ever "next".** Several are genuinely available at once —
  after a race ends the reveal, the awards and the publish are all doable — but
  the screen answers one question, so the earliest ready step wins.
- **The intermission overrides that order**, because it is a hard stop rather
  than a step in a queue.
- **A simulated timer test is marked, not ticked cleanly.** It does not block
  rehearsing a whole night, but it says the real timer has not been tested.

</details>

### M9 — Dress rehearsal on real hardware

1. Launch the `.app` on a clean Mac with no Go, Python or Homebrew.
2. **Run the Timer Test Bench end to end** with the real FastTrack — identify,
   features, gate open/close, per-lane mask, reset, lane mapping, test heat.
   Then break things deliberately: unplug the USB cable mid-session, hold the
   gate half-open, and confirm each is caught with a useful message.
3. Check in cars on the Mac with the USB camera; check a few in from an iPad
   over HTTPS to confirm remote capture works after trusting the certificate.
4. HDMI a TV; confirm the scenes are readable from across a room. **Watch the
   now-racing animation on it** — the cars leaving on the gate and returning in
   finish order has only ever been checked as markup, never seen.
5. Race a real heat; confirm arming, capture, auto-advance and a manual re-run.
   **Then provoke a bad read** — block a finish sensor, or pull a car before the
   line — and confirm closing the start gate ends the heat with that lane at
   9.999, and that racing carries on.
6. Put the impound screen on a second display and load trays from it for a few
   heats; confirm it advances in step with the track.
7. Vote from the tablet; confirm tallies, an undo and a forced tie-break.
8. Publish into a scratch branch of derby-site; run `hugo server` and confirm a
   race page renders identically to a 2026 one.
9. **Hand the run-of-show screen to someone who has never run a race, and have
   them run one.** That is the real acceptance test for this project.

## Since M8

- **Ties for a trophy are run off.** A tie anywhere else stands — two cars that
  ran the same average are the same speed — but 1st, 2nd and 3rd are handed to
  somebody. The run-off is a real heat, armed and timed like any other, whose
  times are deliberately kept out of every average: the rule is four runs, one
  per lane, and a fifth run for two cars would rewrite the averages that tied.
  A run-off that itself finishes level is reported and re-run; the software does
  not pick.
- **The end of the night is ordered deliberately**: voted trophies → reveal
  (handing over 1st, 2nd and 3rd as those cars come up) → run-off → final
  standings → publish. The design and theme trophies go first and on their own,
  because they are about how a car looks; the speed ones are handed over during
  the reveal, which makes the reveal the ceremony rather than a preamble to one;
  and the tie is settled after the reveal, which is where the room learns there
  is one.
- **The speed trophies are derived, not stored.** A stored copy of the top three
  could only ever come to disagree with the standings. Two display scenes were
  added for this: the voted trophies one at a time with the car, and the whole
  settled table for the wrap-up.
- **`scene_shown` exists** because two of those steps — the reveal and the final
  standings — change no result and would otherwise leave the checklist stuck on
  them forever. It records a scene actually being assigned to a display, not a
  button being pressed.
- **The speed trophies are `1st`, `2nd`, `3rd`**, at the club's request, rather
  than "Fastest in Event" as published up to 2026.
- **A heat ends on whichever comes first: every lane reporting, or the start
  gate being closed again.** The club's FastTrack sometimes reports nothing back.
  Any lane the timer never mentioned is recorded as 9.999 rather than dropped,
  and the coordinator is told which lanes were silent. This had to be kept apart
  from the phantom-trigger guard — every lane 9.999 *reported by the timer* still
  stops racing, every lane 9.999 *because nothing was reported* is a bad read and
  is recorded. The simulator can drop a result or a lane to rehearse it.
- **Times can be entered by hand**, on every heat row of the race screen. It is
  how the software is demonstrated with no timer and how a bad reading is
  corrected, and it takes the same path a timer's times do.
- **The now-racing screen animates the heat.** The gate opening clears the
  screen as the cars leave to the right; the results come back in from the left
  in finish order, gold for the winner and red for a non-finish showing its
  9.999. The return is driven by the results arriving rather than literally by
  the gate closing, because the gate is closed to stage the *next* heat, several
  seconds after the times land. Not yet seen on a real TV.
- **An impound screen** for the loading table: the heat on the track and the heat
  to load next, as lanes, pictures and car numbers only. It advances as the race
  does, and `/display?scene=impound` pins a screen to it.
- **Club branding.** The logo is in the coordinator header, top-left of every
  display scene, the favicon, and the `.app` icon — which `make-app.sh` had been
  looking for since M0 without the file ever existing.
- **The Displays page lists each address once**, numeric only, as links with the
  endpoint.
- **Auto-advance is 7 seconds**, at the club's request. The screens follow the
  race, so that one setting is how long the finish order stays up.

## Settled

- **"Raced in a previous championship" is checked from the archive.** The club's
  published championship results are imported from the website folder — six
  years, 130 cars — and a car that matches one is flagged at check-in. It only
  ever flags: excluding a car is a decision and it needs a reason.
- **The Outlaw division** is out of scope for now.
- **The `9*` standby row** in 2026's qualifiers is temporary and is being
  removed from the site, so the publisher writing the committed list only is
  correct. Note that `internal/publish/golden_test.go` and
  `internal/season/golden_test.go` both *assert the fixture still contains it* —
  so if `testdata/2026-qualifiers.csv` is ever refreshed from the corrected
  site, those tests fail loudly with a message saying why, rather than silently
  testing nothing.

- **A championship is a normal race unless it is flagged as a bracket.** Every
  championship the club ran from 2023 to 2025 was four runs a car, fastest
  average wins — 24, 22 and 24 heats, four cars each. The bracket is an option
  chosen when the championship race is created, or switched on the
  championship page until the first heat has times. Switching throws away
  whatever was built for the other format and has not been raced. Only a
  championship can be a bracket: season points come from averages, which a
  bracket does not produce.
- **No PINs.** The settings page offered a coordinator and a crew PIN that
  nothing checked. The fields are gone rather than wired up: the app runs on a
  private network at the venue, and a prompt that can be bypassed by the person
  standing at the laptop protects nothing.

## Open questions

- **Code signing and notarization** — see `packaging/NOTARIZING.md`. Last job
  before anyone else installs the app.
- **Two display scenes are still unbuilt**: the championship bracket, and the
  car-photo slideshow. The bracket is the one that would be noticed.
