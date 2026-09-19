-- Every published race night, read back from the club's own website.
--
-- The club records — fastest run, fastest average, the fastest in each lane,
-- everybody's personal best — only mean something if they go back further than
-- the first night this software ran. The evidence is on the site: heat-by-heat
-- times for every race since 2019. These rows are that, imported rather than
-- typed, and replaced wholesale each time the archive is read again.
--
-- A car that raced but was ruled out keeps its row, marked excluded, because it
-- did go down the track: the rows say what happened and the records decide what
-- counts. The same goes for the pace car.
--
-- A race this software ran itself is never read from here. Once a night has
-- been published, the site holds a copy of it, and counting both would put
-- every run on file twice.

CREATE TABLE archive_car (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    year        INTEGER NOT NULL,
    kind        TEXT    NOT NULL CHECK (kind IN ('points','championship')),
    number      INTEGER NOT NULL,
    car_number  INTEGER NOT NULL,
    first_name  TEXT    NOT NULL DEFAULT '',
    last_name   TEXT    NOT NULL DEFAULT '',
    car_name    TEXT    NOT NULL DEFAULT '',
    place       INTEGER NOT NULL DEFAULT 0,
    is_control  INTEGER NOT NULL DEFAULT 0,
    excluded    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_archive_car_race ON archive_car(year, kind, number);

CREATE TABLE archive_run (
    car_id  INTEGER NOT NULL REFERENCES archive_car(id) ON DELETE CASCADE,
    heat    INTEGER NOT NULL,
    lane    INTEGER NOT NULL,
    time    REAL    NOT NULL,
    PRIMARY KEY (car_id, heat, lane)
);
