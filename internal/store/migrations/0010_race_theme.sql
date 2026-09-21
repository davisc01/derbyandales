-- The night's theme, in the club's words: "Movie Night", "Things That Fly".
--
-- The theme trophy is voted on for how well a car carries the theme, and until
-- now nothing anywhere named it — the ballot asked for the "Best Themed Car"
-- and left the voter to remember what the theme was, which on a sixth pint in
-- a loud room is asking a lot. It belongs to the race rather than the season:
-- each night has its own.

ALTER TABLE race ADD COLUMN theme TEXT NOT NULL DEFAULT '';
