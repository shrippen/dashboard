package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"andon/internal/testkit"
)

// runCLI handles its subcommands, checks their arguments and leaves
// everything else to the server.
func TestRunCLI(t *testing.T) {
	d := testkit.DB(t)
	dataDir := t.TempDir()
	target := t.TempDir()

	if ok, _ := runCLI([]string{"andon"}, d, "", dataDir); ok {
		t.Fatal("no subcommand must start the server")
	}
	if ok, code := runCLI([]string{"andon", "backup"}, d, "", dataDir); !ok || code != 2 {
		t.Fatalf("missing target: ok=%v code=%d", ok, code)
	}

	stdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	ok, code := runCLI([]string{"andon", "backup", target}, d, filepath.Join(dataDir, "andon.db"), dataDir)
	w.Close()
	os.Stdout = stdout
	out, _ := io.ReadAll(r)
	if !ok || code != 0 || !strings.HasSuffix(strings.TrimSpace(string(out)), ".tar.gz") {
		t.Fatalf("backup: ok=%v code=%d out=%q", ok, code, out)
	}
}
