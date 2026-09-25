package sources

// SetBases points the public-API sources at a test server; the returned
// func restores them.
func SetBases(base string) func() {
	saved := []string{nagerBase, jokeBase, coingeckoBase, stooqBase, xkcdBase, nasaBase, flightsBase, transitBase}
	nagerBase, jokeBase, coingeckoBase, stooqBase, xkcdBase, nasaBase, flightsBase, transitBase = base, base, base, base, base, base, base, base
	return func() {
		nagerBase, jokeBase, coingeckoBase, stooqBase = saved[0], saved[1], saved[2], saved[3]
		xkcdBase, nasaBase, flightsBase, transitBase = saved[4], saved[5], saved[6], saved[7]
	}
}
