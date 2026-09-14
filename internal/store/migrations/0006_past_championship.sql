-- Cars that have raced in a previous championship.
--
-- The club's rule is that a car gets one championship. Today that is checked
-- from memory at check-in, which works until the person who remembers is not
-- there — which is the whole reason this project exists.
--
-- These rows are imported from the club's own published archive rather than
-- entered, so there is nothing to keep in step by hand. car_key is the name
-- folded for case and spacing and stripped of the qualifying suffix one year's
-- exporter added, because that is what "the same car" means seven years later.
--
-- This only ever suggests. A car is excluded by a person choosing to exclude
-- it, with a reason; the software's job is to make sure nobody has to remember.

CREATE TABLE past_championship (
    year       INTEGER NOT NULL,
    car_key    TEXT    NOT NULL,
    car_name   TEXT    NOT NULL,
    first_name TEXT    NOT NULL DEFAULT '',
    last_name  TEXT    NOT NULL DEFAULT '',
    car_number INTEGER NOT NULL DEFAULT 0,
    place      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (year, car_key, last_name)
);
CREATE INDEX idx_past_championship_key ON past_championship(car_key);
