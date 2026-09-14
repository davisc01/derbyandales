-- Record *why* an entry was excluded.
--
-- Exclusion is an eligibility decision made at check-in: the car ran in a
-- previous championship, or it does not meet the race rules. The car still
-- races and still appears in the heat results — it is simply left out of the
-- standings, and so cannot take an award, earn wildcard points, or qualify.
--
-- Because exclusion is applied before places are assigned, it changes the
-- result for everyone behind it. In 2026 race 4 the excluded car had the
-- fastest average of the night, so this is not a cosmetic flag.

ALTER TABLE entry ADD COLUMN exclusion_reason TEXT NOT NULL DEFAULT '';
