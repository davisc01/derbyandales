-- How a race is run: a normal race, or a single-elimination bracket.
--
-- The championship used to be assumed to be a bracket. It is not: every
-- published championship from 2019 to 2025 was a normal race — every car four
-- runs, four cars a heat, drop the slowest. The bracket is a format the club
-- can choose for a championship, not something every championship is.
--
-- So format is its own column rather than being inferred from kind. A points
-- race is always normal, because season points come from averages and a bracket
-- does not produce them; that is enforced where races are created, so the
-- message can say why.

ALTER TABLE race ADD COLUMN format TEXT NOT NULL DEFAULT 'standard'
    CHECK (format IN ('standard', 'bracket'));
