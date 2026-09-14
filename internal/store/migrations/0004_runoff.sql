-- Run-offs, for a tie that reaches the podium.
--
-- Two cars that ran the same average are the same speed, and below the top
-- three the club is happy to say so. But a trophy cannot be shared, so a tie
-- for 1st, 2nd or 3rd is settled on the track.
--
-- A run-off is a real heat: it is armed, timed and shown like any other, which
-- is why it lives in the heat table rather than somewhere of its own. What
-- makes it different is that its times are NOT part of anybody's average. The
-- club's rule is four runs, one per lane, drop the slowest — a fifth run for
-- two cars only would quietly rewrite the very averages that tied.
--
-- So runoff_place marks the heat as a decider for that position, and every
-- query that feeds the scoring leaves those heats out. The result is used for
-- one thing: putting the tied cars in order.
--
-- A column rather than a new phase, because phase carries a CHECK constraint
-- and SQLite cannot alter one without rebuilding the table — which would mean
-- dropping and recreating something that heat_lane and bracket_matchup both
-- point at, on a database that holds a season's results.

ALTER TABLE heat ADD COLUMN runoff_place INTEGER;

CREATE INDEX idx_heat_runoff ON heat(race_id, runoff_place);
