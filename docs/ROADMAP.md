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

Today: `6 × 3 + 6 = 24`, `P = 32`, 8 byes, 8 R1 matches, 5 rounds. `N` comes
from **actually seeded entrants**, so a no-show shrinks the field and the bracket
regenerates with one more bye.

**Pairing** uses the standard recursive construction:

```
order(1) = [1]
order(2n) = for each s in order(n): emit s, emit (2n + 1 − s)
```

Consecutive pairs are the R1 matchups; a pair containing a seed > N is a bye.
Later rounds pair consecutive winners by position.

This was verified to reproduce the club's current bracket exactly — same eight
R1 matchups (`16v17, 9v24, 13v20, 12v21, 14v19, 11v22, 10v23, 15v18`), same bye
seeds 1–8, same half-split (1/4/5/8 vs 2/3/6/7). Only the vertical rendering
order of the bottom half differs, which is cosmetic.

**Championship Planner** — show the consequences of a season-shape change before
committing. Verified figures:

| Races | Wildcards | Entrants | Capacity | Byes | R1 matches | Rounds | |
|---|---|---|---|---|---|---|---|
| 4 | 4 | 16 | 16 | 0 | 8 | 4 | bye-free |
| 5 | 1 | 16 | 16 | 0 | 8 | 4 | bye-free |
| 6 | **6** | **24** | **32** | **8** | **8** | **5** | **today** |
| 6 | 14 | 32 | 32 | 0 | 16 | 5 | bye-free |
| 7 | 11 | 32 | 32 | 0 | 16 | 5 | bye-free |
| 8 | 8 | 32 | 32 | 0 | 16 | 5 | bye-free |

Warn when `R1 matches < 2` (the field is mostly byes) or `byes > auto-qualifier
count` (byes would spill onto wildcard racers, contradicting their purpose).

**Seeding auto-populates from season data** — the biggest workflow win. Today it
is a CSV export, a hand-edit to strip a column, an import, then typing 24 seeds.

Seed metadata is decoupled: `origin` ∈ `qualifier | wildcard` set at seeding,
`has_bye` derived from `seed <= byes`. The old model inferred all three from
hardcoded ranges, so a bye racer could not also be labelled an auto-qualifier —
which is what they are.

Each matchup is a two-lane heat on the configured lane pair (default 1 & 2),
swappable while pending. The format diagram should be **generated** from the
computed structure; the existing one is 532 lines of hand-maintained HTML that
would silently lie if the field size changed.

### M8 — Publishing

Settings hold the path to `~/git/derby-site`. "Publish Race N" shows a diff,
then writes. **The app never runs git** — the user reviews and commits.

UTF-8 **without BOM**, LF, comma-delimited. Exact headers:

| Path | Header |
|---|---|
| `content/races/{yyyy}/race-{n}/heats.csv` | `Heat,Lane,FirstName,LastName,CarNumber,CarName,FinishTime,Scale MPH,FinishPlace` |
| `content/races/{yyyy}/race-{n}/standings.csv` | `Place,Car Number,Name,Car Name,Heats,Average,Best,Worst` |
| `content/races/{yyyy}/race-{n}/awards.csv` | `Award Name,Award Type,First Name,Last Name,Car Number,Car Name` |
| `content/races/{yyyy}/season-standings/qualifiers.csv` | `Current Seed,Driver,Car Name,Race,Race Finish,Avg Time,Total Entries,Over Limit` |
| `content/races/{yyyy}/season-standings/wildcard.csv` | `Rank,Name,Total Points` |

Formatting rules taken from the **committed** files, not from the exporter that
produced them — the committed ones were hand-trimmed:

- `heats.csv` uses unspaced `FirstName`/`CarNumber`; `standings.csv` and
  `awards.csv` use spaced `Car Number`/`First Name`. `standings.csv` has a
  single full-name `Name` column.
- `FinishTime` 3 dp, trailing zeros trimmed (`2.76`). `Scale MPH` 1 dp with
  trailing `.0` dropped (`194`). `Average` 3 dp, trailing zeros trimmed
  (`2.43`). `scoring.FormatTime`, `FormatAverage` and `FormatMPH` already do
  this and are asserted against every row of a published race.
- `Avg Time` in `qualifiers.csv` is **3 dp** — the old tracker emitted 4 and
  they were trimmed by hand.
- Drop the old tracker's trailing `Class` column, which was stripped by hand.
- `Over Limit` is exactly `YES` or empty — the site's row highlight matches on it.
- `Total Points` may be the literal string `Max Entries Reached`.
- **Championship pages get `heats.csv` + `standings.csv` only — no `awards.csv`.**
- `index.md` written from derby-site's `archetypes/races.md` if absent; existing
  ones never overwritten.

The site's `csv-table` shortcode tolerates a UTF-8 BOM but the BOM leaks into the
first header cell, breaking its `hide-columns` matching. Write without one.

Also in M8: the **run-of-show screen**, one linear checklist per race night.
This is the answer to the bus-factor problem and should be treated as a feature,
not a nicety:

```
1. Open race        2. Test the timer     3. Check in racers
4. Intros           5. Race (first half)  6. Intermission — voting opens
7. Race (second half)                     8. Results
9. Awards          10. Publish
```

Step 2 sits before check-in deliberately: the timer gets tested while cars are
still being carried in, not after the room is seated.

Step 6 is the club's existing intermission, halfway through the heats — see
the intermission section under M5. The software pauses racing there and opens
voting, rather than relying on the coordinator to remember both.

### M9 — Dress rehearsal on real hardware

1. Launch the `.app` on a clean Mac with no Go, Python or Homebrew.
2. **Run the Timer Test Bench end to end** with the real FastTrack — identify,
   features, gate open/close, per-lane mask, reset, lane mapping, test heat.
   Then break things deliberately: unplug the USB cable mid-session, hold the
   gate half-open, and confirm each is caught with a useful message.
3. Check in cars on the Mac with the USB camera; check a few in from an iPad
   over HTTPS to confirm remote capture works after trusting the certificate.
4. HDMI a TV; confirm the scenes are readable from across a room.
5. Race a real heat; confirm arming, capture, auto-advance and a manual re-run.
6. Vote from the tablet; confirm tallies, an undo and a forced tie-break.
7. Publish into a scratch branch of derby-site; run `hugo server` and confirm a
   race page renders identically to a 2026 one.
8. **Hand the run-of-show screen to someone who has never run a race, and have
   them run one.** That is the real acceptance test for this project.

## Open questions

- **"Raced in a previous championship"** is a season-spanning eligibility rule.
  Starting fresh at 2027 means there is no prior championship to check against
  in year one, so that call stays manual at check-in. From 2028 the app could
  flag candidates automatically.
- **The Outlaw division** appears in the club's written rules but in no CSV and
  no schema. Modelled as nothing so far. Needs a decision before it matters.
- **Code signing and notarization** — see `packaging/NOTARIZING.md`. Worth doing
  before anyone else installs the app.
- **Awards must skip the CONTROL car.** It is ranked in the standings and can
  place first on times, but it takes no trophy. `Entry.EarnsPoints()` encodes
  this. The season side now honours it; the awards side still has to, in M8.
- **The `9*` standby row** in the published qualifiers list — see M6 above.
  M8 has to decide whether to reproduce it or publish the committed list only.
