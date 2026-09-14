-- Frozen race results, which are what the season is computed from.
--
-- A race's standings are derived from heat times and so can change: a re-run,
-- an ignored lane, a correction the morning after. The season must not move
-- underneath people that way. Six weeks later a racer should be able to ask why
-- they got 14 points and get an answer, not a number recomputed under whatever
-- the settings happen to say today.
--
-- So when a race completes, its result is frozen here: place, average and the
-- points that follow from them, together with the field size they were computed
-- against. Everything season-wide reads these rows. Recomputing is an explicit,
-- audited action rather than a side effect of changing a setting.
--
-- Excluded cars are absent — they never reach the standings. The CONTROL pace
-- car is present with zero points, because it is ranked and it is part of the
-- night's record; it is simply not a competitor.

CREATE TABLE race_result (
    race_id      INTEGER NOT NULL REFERENCES race(id)  ON DELETE CASCADE,
    entry_id     INTEGER NOT NULL REFERENCES entry(id) ON DELETE CASCADE,
    racer_id     INTEGER NOT NULL REFERENCES racer(id) ON DELETE CASCADE,

    place        INTEGER NOT NULL,   -- 0 when the car recorded no usable run
    average      REAL    NOT NULL,
    heats        INTEGER NOT NULL,

    points       INTEGER NOT NULL,
    -- The racer count the points were computed against, kept so the arithmetic
    -- stays explainable after the rules or the roster have moved on.
    total_racers INTEGER NOT NULL,

    frozen_at    INTEGER NOT NULL,
    PRIMARY KEY (race_id, entry_id)
);
CREATE INDEX idx_race_result_racer ON race_result(racer_id);
