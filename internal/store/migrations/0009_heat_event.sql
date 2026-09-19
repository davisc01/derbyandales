-- What happened to each heat besides being run: re-run, typed in by hand, a
-- bad read, the timer firing with nothing on the track.
--
-- The audit log records some of these, but as "heat 7" with no race, which is
-- enough for a person reading it and not enough to count a night's trouble
-- for the wrap-up. The wrap-up's timer health is a question the people who
-- look after the track ask at the end of every night: was it the timer, and
-- which lane. These rows answer it.

CREATE TABLE heat_event (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    race_id     INTEGER NOT NULL REFERENCES race(id) ON DELETE CASCADE,
    heat_number INTEGER NOT NULL,
    kind        TEXT    NOT NULL CHECK (kind IN ('rerun','manual','bad_read','false_trigger')),
    lanes       TEXT    NOT NULL DEFAULT '',
    at          INTEGER NOT NULL
);
CREATE INDEX idx_heat_event_race ON heat_event(race_id);
