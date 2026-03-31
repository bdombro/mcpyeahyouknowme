package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoogleDocsReset_noDataDir(t *testing.T) {
	dDir := filepath.Join(t.TempDir(), "nonexistent")
	plist := filepath.Join(t.TempDir(), "fake.plist")

	googleDocsReset(dDir, plist)
}

func TestGoogleDocsReset_removesOnlyGoogleDocsFiles(t *testing.T) {
	dDir := t.TempDir()
	plist := filepath.Join(t.TempDir(), "fake.plist")

	gdToken := filepath.Join(dDir, "googledocs_token.json")
	gdEmail := filepath.Join(dDir, "googledocs_email.txt")
	gdDB := filepath.Join(dDir, "googledocs.db")
	waDB := filepath.Join(dDir, "whatsapp.db")
	msgDB := filepath.Join(dDir, "messages.db")

	for _, f := range []string{gdToken, gdEmail, gdDB, waDB, msgDB} {
		if err := os.WriteFile(f, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	googleDocsReset(dDir, plist)

	for _, f := range []string{gdToken, gdEmail, gdDB} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed", filepath.Base(f))
		}
	}
	for _, f := range []string{waDB, msgDB} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("expected %s to be preserved, got err: %v", filepath.Base(f), err)
		}
	}
}

func TestGoogleDocsReset_preservesPlist(t *testing.T) {
	dDir := t.TempDir()
	plistDir := t.TempDir()
	plist := filepath.Join(plistDir, "com.test.plist")

	if err := os.WriteFile(plist, []byte("<plist/>"), 0o600); err != nil {
		t.Fatal(err)
	}

	googleDocsReset(dDir, plist)

	if _, err := os.Stat(plist); os.IsNotExist(err) {
		t.Error("plist file should be preserved after googledocs reset")
	}
}

func TestGoogleDocsReset_toleratesMissingFiles(t *testing.T) {
	dDir := t.TempDir()
	plist := filepath.Join(t.TempDir(), "fake.plist")

	googleDocsReset(dDir, plist)
}

func TestGoogleDocsReset_warnsOnRemoveError(t *testing.T) {
	dDir := t.TempDir()
	plist := filepath.Join(t.TempDir(), "fake.plist")

	// Create a non-empty directory at the db path — os.Remove will fail with
	// a non-IsNotExist error (ENOTEMPTY), exercising the warning branch.
	dbDir := filepath.Join(dDir, "googledocs.db")
	if err := os.MkdirAll(filepath.Join(dbDir, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	googleDocsReset(dDir, plist)

	if _, err := os.Stat(dbDir); os.IsNotExist(err) {
		t.Error("non-empty dir should survive failed os.Remove")
	}
}
