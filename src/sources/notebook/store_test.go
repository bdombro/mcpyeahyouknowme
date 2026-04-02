package notebook

import (
	"testing"
	"time"
)

// Verifies GetCacheRow returns nil for a path that has not been cached.
func TestGetCacheRow_miss(t *testing.T) {
	db := newTestDB(t)
	row, err := GetCacheRow(db, "/nonexistent/path.md")
	if err != nil {
		t.Fatalf("GetCacheRow: %v", err)
	}
	if row != nil {
		t.Fatalf("expected nil for uncached path, got %+v", row)
	}
}

// Verifies UpsertCacheRow inserts a new row and GetCacheRow retrieves it with matching fields.
func TestUpsertCacheRow_insert(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().Unix()
	r := CacheRow{
		Path:     "/notes/hello.md",
		Dir:      "/notes",
		ModTime:  now - 100,
		Size:     512,
		FileType: "md",
		Title:    "Hello",
		Content:  "Hello world",
		Labels:   "[]",
		CachedAt: now,
	}
	if err := UpsertCacheRow(db, r); err != nil {
		t.Fatalf("UpsertCacheRow: %v", err)
	}
	got, err := GetCacheRow(db, "/notes/hello.md")
	if err != nil {
		t.Fatalf("GetCacheRow: %v", err)
	}
	if got == nil {
		t.Fatal("expected row, got nil")
	}
	if got.Title != "Hello" || got.Content != "Hello world" || got.FileType != "md" {
		t.Fatalf("unexpected row: %+v", got)
	}
}

// Verifies UpsertCacheRow replaces an existing row when the file has changed.
func TestUpsertCacheRow_replace(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().Unix()
	r := CacheRow{Path: "/notes/hello.md", Dir: "/notes", ModTime: now, Size: 100, FileType: "md", Title: "Old", Content: "old content", Labels: "[]", CachedAt: now}
	if err := UpsertCacheRow(db, r); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	r.Title = "New"
	r.Content = "new content"
	r.ModTime = now + 10
	if err := UpsertCacheRow(db, r); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got, _ := GetCacheRow(db, "/notes/hello.md")
	if got.Title != "New" || got.Content != "new content" {
		t.Fatalf("expected updated row, got %+v", got)
	}
}

// Verifies AllCachePathsForDir returns only paths belonging to the given directory.
func TestAllCachePathsForDir(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().Unix()
	seed := func(path, dir string) {
		UpsertCacheRow(db, CacheRow{Path: path, Dir: dir, ModTime: now, Size: 1, FileType: "md", Title: "T", Content: "C", Labels: "[]", CachedAt: now})
	}
	seed("/a/one.md", "/a")
	seed("/a/two.md", "/a")
	seed("/b/three.md", "/b")

	paths, err := AllCachePathsForDir(db, "/a")
	if err != nil {
		t.Fatalf("AllCachePathsForDir: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths for /a, got %d: %v", len(paths), paths)
	}
}

// Verifies PruneStaleEntries deletes rows for paths no longer in the active set.
func TestPruneStaleEntries(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().Unix()
	seed := func(path string) {
		UpsertCacheRow(db, CacheRow{Path: path, Dir: "/notes", ModTime: now, Size: 1, FileType: "md", Title: "T", Content: "C", Labels: "[]", CachedAt: now})
	}
	seed("/notes/keep.md")
	seed("/notes/delete.md")

	active := map[string]bool{"/notes/keep.md": true}
	if err := PruneStaleEntries(db, "/notes", active); err != nil {
		t.Fatalf("PruneStaleEntries: %v", err)
	}

	kept, _ := GetCacheRow(db, "/notes/keep.md")
	deleted, _ := GetCacheRow(db, "/notes/delete.md")
	if kept == nil {
		t.Fatal("expected /notes/keep.md to remain")
	}
	if deleted != nil {
		t.Fatal("expected /notes/delete.md to be pruned")
	}
}

// Verifies PruneDir removes all cache entries for a directory.
func TestPruneDir(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().Unix()
	UpsertCacheRow(db, CacheRow{Path: "/x/a.md", Dir: "/x", ModTime: now, Size: 1, FileType: "md", Title: "A", Content: "", Labels: "[]", CachedAt: now})
	UpsertCacheRow(db, CacheRow{Path: "/x/b.md", Dir: "/x", ModTime: now, Size: 1, FileType: "md", Title: "B", Content: "", Labels: "[]", CachedAt: now})
	UpsertCacheRow(db, CacheRow{Path: "/y/c.md", Dir: "/y", ModTime: now, Size: 1, FileType: "md", Title: "C", Content: "", Labels: "[]", CachedAt: now})

	if err := PruneDir(db, "/x"); err != nil {
		t.Fatalf("PruneDir: %v", err)
	}
	paths, _ := AllCachePathsForDir(db, "/x")
	if len(paths) != 0 {
		t.Fatalf("expected 0 paths for /x after prune, got %d", len(paths))
	}
	paths, _ = AllCachePathsForDir(db, "/y")
	if len(paths) != 1 {
		t.Fatalf("expected /y to be untouched, got %d paths", len(paths))
	}
}

// Verifies UpsertCacheRow auto-populates CachedAt when the caller passes zero.
func TestUpsertCacheRow_autoCachedAt(t *testing.T) {
	db := newTestDB(t)
	r := CacheRow{Path: "/p.md", Dir: "/", ModTime: 1, Size: 1, FileType: "md", Title: "T", Content: "", Labels: "[]", CachedAt: 0}
	if err := UpsertCacheRow(db, r); err != nil {
		t.Fatalf("UpsertCacheRow: %v", err)
	}
	got, _ := GetCacheRow(db, "/p.md")
	if got.CachedAt == 0 {
		t.Fatal("expected CachedAt to be set automatically")
	}
}
