-- A placed tile may span more than one row of its section's grid.
ALTER TABLE placements ADD COLUMN rows INTEGER NOT NULL DEFAULT 1;
