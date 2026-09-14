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

This is not a data error — it is a real thing the club does, and the reason
`entry.excluded` exists in the schema. It also mattered: **car 901 had the
fastest average of the night (2.353)**, so excluding it is what made car 73
(Duncan Breland, "Loose Moose", 2.367) the winner of record.

The same pattern appears in 2026 race 3, where car 89 and a second Derby Ales
car were excluded. Races 1, 2 and 5 excluded nobody.

`2026-race-4-excluded.txt` lists the excluded car numbers so the golden test can
apply the exclusion the way the app will.
