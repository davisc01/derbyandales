-- Initial schema.
--
-- Timestamps are Unix seconds (INTEGER). Booleans are INTEGER 0/1.
-- Schedule and results share a table (heat_lane), so "finish_time IS NULL"
-- is the universal "not yet run" predicate.

CREATE TABLE season (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    year                     INTEGER NOT NULL UNIQUE,
    name                     TEXT    NOT NULL DEFAULT '',
    lane_count               INTEGER NOT NULL DEFAULT 4,
    track_length_ft          REAL    NOT NULL DEFAULT 28,
    scale_denom              INTEGER NOT NULL DEFAULT 25,

    -- Championship shape. Field size, byes and rounds all derive from these.
    race_count               INTEGER NOT NULL DEFAULT 6,
    auto_qual_places         INTEGER NOT NULL DEFAULT 3,
    wildcard_spots           INTEGER NOT NULL DEFAULT 6,
    max_championship_entry   INTEGER NOT NULL DEFAULT 3,

    -- The pace car scores no points and should not inflate the field size.
    -- 0 = excluded from total_racers (corrected behaviour).
    points_count_control     INTEGER NOT NULL DEFAULT 0,

    -- Which two lanes head-to-head bracket heats use.
    bracket_lane_a           INTEGER NOT NULL DEFAULT 1,
    bracket_lane_b           INTEGER NOT NULL DEFAULT 2,

    created_at               INTEGER NOT NULL
);

CREATE TABLE racer (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    season_id  INTEGER NOT NULL REFERENCES season(id) ON DELETE CASCADE,
    first_name TEXT NOT NULL DEFAULT '',
    last_name  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_racer_season ON racer(season_id);
CREATE UNIQUE INDEX idx_racer_name ON racer(season_id, first_name, last_name);

CREATE TABLE race (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    season_id  INTEGER NOT NULL REFERENCES season(id) ON DELETE CASCADE,
    number     INTEGER NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    date       INTEGER NOT NULL,
    venue      TEXT NOT NULL DEFAULT '',
    kind       TEXT NOT NULL CHECK (kind IN ('points','championship')),
    status     TEXT NOT NULL CHECK (status IN ('setup','checkin','racing','voting','complete')),
    created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_race_season_number ON race(season_id, kind, number);

CREATE TABLE photo (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    sha256     TEXT NOT NULL UNIQUE,
    ext        TEXT NOT NULL,
    width      INTEGER NOT NULL DEFAULT 0,
    height     INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE TABLE entry (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    race_id       INTEGER NOT NULL REFERENCES race(id) ON DELETE CASCADE,
    racer_id      INTEGER NOT NULL REFERENCES racer(id) ON DELETE CASCADE,
    car_number    INTEGER NOT NULL,
    car_name      TEXT NOT NULL DEFAULT '',
    photo_id      INTEGER REFERENCES photo(id),
    is_control    INTEGER NOT NULL DEFAULT 0,
    excluded      INTEGER NOT NULL DEFAULT 0,
    checked_in_at INTEGER,
    note          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_entry_race ON entry(race_id);
CREATE UNIQUE INDEX idx_entry_car_number ON entry(race_id, car_number);

CREATE TABLE heat (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    race_id            INTEGER NOT NULL REFERENCES race(id) ON DELETE CASCADE,
    number             INTEGER NOT NULL,
    phase              TEXT NOT NULL CHECK (phase IN ('qualifying','bracket')),
    bracket_matchup_id INTEGER,
    status             TEXT NOT NULL CHECK (status IN ('pending','armed','running','complete')),
    armed_at           INTEGER,
    completed_at       INTEGER
);
CREATE UNIQUE INDEX idx_heat_race_number ON heat(race_id, number);

CREATE TABLE heat_lane (
    heat_id      INTEGER NOT NULL REFERENCES heat(id) ON DELETE CASCADE,
    lane         INTEGER NOT NULL,
    entry_id     INTEGER REFERENCES entry(id) ON DELETE CASCADE,
    finish_time  REAL,
    finish_place INTEGER,
    ignored      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (heat_id, lane)
);
CREATE INDEX idx_heat_lane_entry ON heat_lane(entry_id);

CREATE TABLE award (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    race_id    INTEGER NOT NULL REFERENCES race(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    award_type TEXT NOT NULL DEFAULT '',
    entry_id   INTEGER REFERENCES entry(id) ON DELETE SET NULL,
    sort       INTEGER NOT NULL DEFAULT 0,
    source     TEXT NOT NULL CHECK (source IN ('auto','vote','manual'))
);
CREATE INDEX idx_award_race ON award(race_id);

CREATE TABLE vote_category (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    race_id         INTEGER NOT NULL REFERENCES race(id) ON DELETE CASCADE,
    key             TEXT NOT NULL,
    label           TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1,
    sort            INTEGER NOT NULL DEFAULT 0,
    winner_entry_id INTEGER REFERENCES entry(id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX idx_vote_category_race_key ON vote_category(race_id, key);

CREATE TABLE vote (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    category_id INTEGER NOT NULL REFERENCES vote_category(id) ON DELETE CASCADE,
    entry_id    INTEGER NOT NULL REFERENCES entry(id) ON DELETE CASCADE,
    cast_at     INTEGER NOT NULL,
    voided      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_vote_category ON vote(category_id, voided);

CREATE TABLE bracket_seed (
    race_id  INTEGER NOT NULL REFERENCES race(id) ON DELETE CASCADE,
    entry_id INTEGER NOT NULL REFERENCES entry(id) ON DELETE CASCADE,
    seed     INTEGER NOT NULL,
    origin   TEXT NOT NULL CHECK (origin IN ('qualifier','wildcard')),
    PRIMARY KEY (race_id, entry_id)
);
CREATE UNIQUE INDEX idx_bracket_seed_unique ON bracket_seed(race_id, seed);

CREATE TABLE bracket_matchup (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    race_id         INTEGER NOT NULL REFERENCES race(id) ON DELETE CASCADE,
    round           INTEGER NOT NULL,
    position        INTEGER NOT NULL,
    top_entry_id    INTEGER REFERENCES entry(id) ON DELETE SET NULL,
    bottom_entry_id INTEGER REFERENCES entry(id) ON DELETE SET NULL,
    winner_entry_id INTEGER REFERENCES entry(id) ON DELETE SET NULL,
    heat_id         INTEGER REFERENCES heat(id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX idx_bracket_matchup_pos ON bracket_matchup(race_id, round, position);

CREATE TABLE adjustment (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    season_id  INTEGER NOT NULL REFERENCES season(id) ON DELETE CASCADE,
    racer_id   INTEGER NOT NULL REFERENCES racer(id) ON DELETE CASCADE,
    points     INTEGER NOT NULL,
    reason     TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE qualifier_substitution (
    season_id           INTEGER NOT NULL REFERENCES season(id) ON DELETE CASCADE,
    original_entry_id   INTEGER NOT NULL UNIQUE REFERENCES entry(id) ON DELETE CASCADE,
    substitute_entry_id INTEGER NOT NULL UNIQUE REFERENCES entry(id) ON DELETE CASCADE,
    created_at          INTEGER NOT NULL
);

CREATE TABLE display (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    token        TEXT NOT NULL UNIQUE,
    page         TEXT NOT NULL DEFAULT 'blank',
    params       TEXT NOT NULL DEFAULT '{}',
    last_seen_at INTEGER NOT NULL
);

CREATE TABLE setting (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE audit (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    at     INTEGER NOT NULL,
    actor  TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_audit_at ON audit(at);
