-- Which scenes a race has actually put on a screen.
--
-- Two steps of the run of show leave no other trace: revealing the results and
-- putting the final standings up for the wrap-up. Both are things somebody
-- does, neither changes a result, and without this the checklist would sit on
-- them forever — which is exactly when a novice coordinator needs it to move on.
--
-- The alternative was a tick box, and a tick box is a lie waiting to happen: it
-- records that somebody pressed a button, not that a TV showed anything. This
-- records the scene actually being assigned to a display.
--
-- One row per race and scene. Showing the reveal twice is the same fact.

CREATE TABLE scene_shown (
    race_id INTEGER NOT NULL REFERENCES race(id) ON DELETE CASCADE,
    scene   TEXT    NOT NULL,
    at      INTEGER NOT NULL,
    PRIMARY KEY (race_id, scene)
);
