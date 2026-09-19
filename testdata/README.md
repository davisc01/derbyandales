# Test fixtures

Real published results from the club's website, used as golden files. If the
scoring engine stops reproducing these, it has diverged from the results the
club has actually been racing on.

## `2026-race-4-*.csv`

MidSouth Derby and Ales, 2026 race 4, copied verbatim from
`derby-site/content/races/2026/race-4/`. They carry the UTF-8 BOM the DerbyNet
export produced, which is why the test strips it.

- `2026-race-4-heats.csv` — 99 lane results across 25 heats.
- `2026-race-4-standings.csv` — the 23-car standings DerbyNet computed from them.

### Excluded entries

25 cars appear in the heats but only 23 in the standings. Two entries were
excluded from scoring:

| Car | Driver | Car name |
|---|---|---|
| 89 | Justin Palmer | Solo Jazz |
| 901 | Justin Palmer | Shelby |

This is not a data error. Exclusion is an eligibility decision made at check-in:
the car ran in a previous championship, or it does not meet the race rules. An
excluded car still races and still appears in the heat results — it is simply
left out of the standings, and so takes no award, earns no wildcard points, and
cannot qualify.

Because the exclusion is applied before places are assigned, it moves everyone
behind it up, which is why the published standings number cleanly 1–23. It also
decided this race: **car 901 had the fastest average of the night (2.353)**, so
excluding it is what made car 73 (Duncan Breland, "Loose Moose", 2.367) the
winner of record.

Note the contrast with the club's CONTROL pace car, which is a different thing.
Car 1 (Derby Ales, "CONTROL") *is* in the standings, at place 23 — it races and
is ranked, but earns no wildcard points.

The same pattern appears in 2026 race 3, where car 89 and a second Derby Ales
car were excluded. Races 1, 2 and 5 excluded nobody.

`2026-race-4-excluded.txt` lists the excluded car numbers so the golden test can
apply the exclusion the way the app will.

## The 2026 season files

`2026-race-{1..5}-standings.csv` are the five nights the club had published when
this was written, copied verbatim from `derby-site/content/races/2026/`. Races 2,
3 and 5 were published without the `Heats` column, so the golden test reads the
headers rather than assuming positions.

`2026-wildcard.csv` and `2026-qualifiers.csv` are the season standings the old
tracker produced from those five races. `internal/season/golden_test.go` rebuilds
both from the race files. Two rows are expected **not** to match, and the test
says which and why rather than skipping them:

- **`Derby Ales`, ranked 36th on zero points** in `2026-wildcard.csv` is the
  CONTROL pace car's driver. There is no such person. The old tracker built its
  roster from every standings row, so the pace car became a competitor.
- **The row seeded `9*`** in `2026-qualifiers.csv` was added by hand. It is the
  standby for an over-limit racer — shown alongside the slot rather than
  replacing it, because mid-season the substitution had not been committed.

Everything else matches exactly, including the published points totals, which
were produced with the pace car counted in the field size. That is what
`Rules.CountControl` restores; the corrected default costs each racer one point
per race they scored in, and the test asserts precisely that difference.

Note these two files carry **no BOM** — they came from the Python tracker, not
the DerbyNet export.


## `championships/`

The club's published championship standings for 2019 and 2021-2025, copied from
`derby-site/content/races/{year}/championship/standings.csv`. They are the
evidence for the rule that a car gets one championship.

They are also a fair sample of what the archive actually looks like. Four
different exporters over seven years:

- **2019** wrapped two header cells across lines, so `Average\nTime` is one
  column name. Folding whitespace is what makes the rest of the row findable.
- **2021-2024** use `Last Name,First Name`; **2025** uses one `Name` column.
- **2021** appended the qualifying origin to every car name — `-1` through `-4`
  for the race it qualified from, `-W` for a wildcard. That is not part of the
  name and is stripped. The stripping has to be narrow: 2024 has a car called
  **Drive-By**.
- 2020 is absent, and 2026's championship has not been run.

## `races/`

Six published race nights, heats and standings, copied verbatim from
`derby-site/content/races/`. They feed the club records, and were picked for
what each one does to a reader:

- **2019/race-1** — header cells wrapped across lines; `Last Name,First Name`.
- **2021/race-4** — a non-finish written as `9.9999`.
- **2023/race-1** — no car numbers in the standings, racers with two cars each,
  and a disqualified car marked by `- DQ` on its name. Only the average tells
  Chris Bryan's two cars apart.
- **2025/race-1** — a typo in the heats ("Ameila") that the standings get right,
  and a car that raced but is not in the standings.
- **2025/championship** — the same, in a championship.
- **2026/race-4** — two excluded cars missing from the standings, one of them
  the fastest average of the night.
