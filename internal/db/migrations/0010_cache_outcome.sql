-- The cache row keeps only the fetch outcome; the dataset copy was never
-- read back and held service data (transactions, visits) on disk.
UPDATE cache SET data = NULL;
