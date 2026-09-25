package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	"dashboard/internal/services/maintenance"
)

const cliUsage = `usage:
  dashboard                          run the server
  dashboard backup <dir>             SQLite backup as tar.gz
  dashboard rotate-key <keyfile>     re-encrypt secrets under a new master key

"dashboard import" (Python's YAML/Dashy importer) is not ported: it needs
app/services/porting.py, which this rewrite doesn't have yet.`

// runCLI handles the operator subcommands (backup, rotate-key); ok=false
// means argv wasn't one of them, so main should start the server instead.
func runCLI(argv []string, database *sql.DB, dbPath string) (ok bool, exitCode int) {
	if len(argv) < 2 {
		return false, 0
	}

	switch argv[1] {
	case "backup":
		if len(argv) != 3 {
			fmt.Fprintln(os.Stderr, cliUsage)
			return true, 2
		}
		archive, err := maintenance.Backup(database, dbPath, argv[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "backup:", err)
			return true, 1
		}
		fmt.Println(archive)
		return true, 0

	case "rotate-key":
		if len(argv) != 3 {
			fmt.Fprintln(os.Stderr, cliUsage)
			return true, 2
		}
		raw, err := os.ReadFile(argv[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "rotate-key:", err)
			return true, 1
		}
		count, err := maintenance.RotateKey(database, strings.TrimSpace(string(raw)))
		if err != nil {
			fmt.Fprintln(os.Stderr, "rotate-key:", err)
			return true, 1
		}
		fmt.Printf("%d secrets re-encrypted. Replace the master_key secret and restart.\n", count)
		return true, 0

	case "import":
		fmt.Fprintln(os.Stderr, "dashboard import: not ported yet (needs app/services/porting.py)")
		return true, 1

	case "-h", "--help", "help":
		fmt.Println(cliUsage)
		return true, 0
	}

	return false, 0
}
