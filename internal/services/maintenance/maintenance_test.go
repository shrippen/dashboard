package maintenance_test

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"andon/internal/crypto"
	"andon/internal/db"
	"andon/internal/db/dbtest"
	"andon/internal/enums"
	"andon/internal/model"
	"andon/internal/repos/content"
	"andon/internal/services/maintenance"
)

func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	crypto.Init("test-master-key")
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path, dbtest.Key)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d, path
}

func TestBackupProducesReadableArchive(t *testing.T) {
	d, path := openTestDB(t)
	target := t.TempDir()

	archive, err := maintenance.Backup(d, path, target, "")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	f, err := os.Open(archive)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	tr := tar.NewReader(gz)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("tar entry: %v", err)
	}
	if hdr.Name != "andon.db" || hdr.Size == 0 {
		t.Fatalf("unexpected archive entry: %+v", hdr)
	}
}

func TestRotateKeyReEncryptsConnectionSecret(t *testing.T) {
	d, path := openTestDB(t)
	sp := &model.Space{Kind: enums.SpacePersonal, Name: "x", Version: 1}
	if err := content.AddSpace(d, sp); err != nil {
		t.Fatalf("add space: %v", err)
	}
	enc, err := crypto.Encrypt("s3cret-token", crypto.PurposeCredential, nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	conn := &model.Connection{
		SpaceID: sp.ID, Key: "kimai", Name: "Kimai", Service: "kimai", URL: "https://kimai.example",
		CredentialMode: enums.CredentialShared, SecretEnc: enc, VerifyTLS: true, CreatedAt: time.Now().UTC(),
	}
	if err := content.AddConnection(d, conn); err != nil {
		t.Fatalf("add connection: %v", err)
	}

	count, err := maintenance.RotateKey(d, path, "new-master-key")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 secret re-encrypted, got %d", count)
	}

	updated, err := content.Connection(d, conn.ID)
	if err != nil {
		t.Fatalf("reload connection: %v", err)
	}
	// The old master key must no longer decrypt it...
	crypto.Init("test-master-key")
	if _, err := crypto.Decrypt(updated.SecretEnc, crypto.PurposeCredential); err == nil {
		t.Fatal("expected the old master key to no longer decrypt the rotated secret")
	}
	// ...but the new one must.
	crypto.Init("new-master-key")
	text, err := crypto.Decrypt(updated.SecretEnc, crypto.PurposeCredential)
	if err != nil || text != "s3cret-token" {
		t.Fatalf("expected the new master key to decrypt to the original secret, got %q err=%v", text, err)
	}

	// The next start with the new master key opens the rekeyed file.
	d.Close()
	fileKey, err := crypto.DatabaseKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(path, fileKey)
	if err != nil {
		t.Fatalf("open with new file key: %v", err)
	}
	defer reopened.Close()
	if _, err := content.Connection(reopened, conn.ID); err != nil {
		t.Fatalf("connection after rekey: %v", err)
	}
}

// The archive carries uploaded icons and theme files next to the
// database: they only exist on disk and are gone otherwise.
func TestBackupIncludesAssets(t *testing.T) {
	d, path := openTestDB(t)
	dataDir := t.TempDir()
	for _, f := range []string{"icons/ab/icon.png", "themes/7/Inter-600.woff2"} {
		p := filepath.Join(dataDir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o640); err != nil {
			t.Fatal(err)
		}
	}

	archive, err := maintenance.Backup(d, path, t.TempDir(), dataDir)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		names[hdr.Name] = true
	}
	for _, want := range []string{"andon.db", "icons/ab/icon.png", "themes/7/Inter-600.woff2"} {
		if !names[want] {
			t.Errorf("missing %s in %v", want, names)
		}
	}
}
