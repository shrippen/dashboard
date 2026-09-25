-- Section layout: width in quarters of the main column (0 = full),
-- height in grid rows (0 = one), accent color (theme token name).
ALTER TABLE sections ADD COLUMN span INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sections ADD COLUMN row_span INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sections ADD COLUMN color TEXT NOT NULL DEFAULT '';
