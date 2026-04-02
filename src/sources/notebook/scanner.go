package notebook

import (
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mcpyeahyouknowme/core"
)

// supportedExts lists all file extensions the notebook source processes.
var supportedExts = map[string]string{
	".md":   "md",
	".txt":  "md", // treat plain text like markdown
	".pdf":  "pdf",
	".jpg":  "image",
	".jpeg": "image",
	".png":  "image",
	".gif":  "image",
	".webp": "image",
	".heic": "image",
	".heif": "image",
	".tiff": "image",
	".tif":  "image",
	".bmp":  "image",
}

// fileTypeOf returns the logical file type ("md", "pdf", "image") for a filename, or "" if unsupported.
func fileTypeOf(name string) string {
	return supportedExts[strings.ToLower(filepath.Ext(name))]
}

// scanDirs walks all configured directories, using the file cache to skip unchanged files,
// and returns SearchEntry values suitable for the global search index.
func scanDirs(db *sql.DB, dirs []string, sourceName string) ([]core.SearchEntry, error) {
	analyzer := VisionAnalyzer{}
	var all []core.SearchEntry
	for _, dir := range dirs {
		entries, err := scanOneDir(db, dir, sourceName, analyzer)
		if err != nil {
			continue
		}
		all = append(all, entries...)
	}
	return all, nil
}

// scanOneDir walks a single directory, updating the cache for new/changed files and pruning stale entries.
func scanOneDir(db *sql.DB, dir, sourceName string, analyzer ImageAnalyzer) ([]core.SearchEntry, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}

	activePaths := map[string]bool{}
	var entries []core.SearchEntry

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil { // nocov — filesystem race during walk
			return nil
		}
		if d.IsDir() {
			// Skip hidden directories (e.g. .git, .obsidian).
			if strings.HasPrefix(d.Name(), ".") && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		fileType := fileTypeOf(d.Name())
		if fileType == "" {
			return nil
		}

		info, err := d.Info()
		if err != nil { // nocov — filesystem race between WalkDir and Info
			return nil
		}

		activePaths[path] = true
		modTime := info.ModTime().Unix()
		size := info.Size()

		cached, err := GetCacheRow(db, path)
		if err != nil { // nocov — DB error on valid schema
			return nil
		}

		var row CacheRow
		if cached != nil && cached.ModTime == modTime && cached.Size == size {
			// Cache hit: use existing extraction results.
			row = *cached
		} else {
			// Cache miss: extract and store.
			row = extractFile(path, dir, fileType, modTime, size, analyzer)
			_ = UpsertCacheRow(db, row)
		}

		fileEntries := buildSearchEntries(row, sourceName)
		entries = append(entries, fileEntries...)
		return nil
	})
	if err != nil { // nocov — WalkDir on a stat-verified dir
		return nil, err
	}

	_ = PruneStaleEntries(db, dir, activePaths)
	return entries, nil
}

// extractFile extracts title, content, and labels from a file based on its type.
func extractFile(path, dir, fileType string, modTime, size int64, analyzer ImageAnalyzer) CacheRow {
	row := CacheRow{
		Path:     path,
		Dir:      dir,
		ModTime:  modTime,
		Size:     size,
		FileType: fileType,
		Labels:   "[]",
		CachedAt: time.Now().Unix(),
	}

	switch fileType {
	case "md":
		title, content, err := extractMarkdown(path)
		if err != nil {
			row.Title = stemName(filepath.Base(path))
			return row
		}
		row.Title = title
		row.Content = content

	case "pdf":
		title, content, _ := extractPDF(path, analyzer)
		row.Title = title
		row.Content = content

	case "image":
		row.Title = stemName(filepath.Base(path))
		if analyzer != nil {
			ocrText, labels, err := analyzer.AnalyzeImage(path)
			if err == nil {
				row.Content = ocrText
				if len(labels) > 0 {
					data, _ := json.Marshal(labels)
					row.Labels = string(data)
				}
			}
		}
	}

	return row
}

// buildSearchEntries converts a cache row into one or more SearchEntry values, chunking large text content.
func buildSearchEntries(row CacheRow, sourceName string) []core.SearchEntry {
	relPath := relativePath(row.Dir, row.Path)
	baseMeta, _ := json.Marshal(map[string]string{"path": relPath, "dir": row.Dir})

	switch row.FileType {
	case "md":
		return buildTextEntries(row, sourceName, "note_title", "note_content", baseMeta)
	case "pdf":
		return buildTextEntries(row, sourceName, "pdf_title", "pdf_content", baseMeta)
	case "image":
		content := row.Content
		if row.Labels != "" && row.Labels != "[]" {
			var labels []string
			if err := json.Unmarshal([]byte(row.Labels), &labels); err == nil && len(labels) > 0 {
				content = strings.Join(labels, " ") + " " + content
			}
		}
		meta, _ := json.Marshal(map[string]string{"path": relPath, "dir": row.Dir, "labels": row.Labels})
		ts := unixToTime(row.ModTime)
		return []core.SearchEntry{{
			Source:      sourceName,
			SourceID:    row.Path,
			ContentType: "image",
			Title:       row.Title,
			Content:     strings.TrimSpace(content),
			Metadata:    meta,
			Timestamp:   &ts,
		}}
	}
	return nil
}

// buildTextEntries emits a title entry plus chunked content entries for markdown and PDF files.
func buildTextEntries(row CacheRow, sourceName, titleType, contentType string, baseMeta json.RawMessage) []core.SearchEntry {
	relPath := relativePath(row.Dir, row.Path)
	ts := unixToTime(row.ModTime)
	var entries []core.SearchEntry

	entries = append(entries, core.SearchEntry{
		Source:      sourceName,
		SourceID:    row.Path + "#title",
		ContentType: titleType,
		Title:       row.Title,
		Content:     row.Title,
		Metadata:    baseMeta,
		Timestamp:   &ts,
	})

	if strings.TrimSpace(row.Content) == "" {
		return entries
	}

	content := cleanMarkdownForIndex(row.Content)
	chunks := chunkText(content)
	for i, chunk := range chunks {
		chunkMeta, _ := json.Marshal(map[string]interface{}{"path": relPath, "dir": row.Dir, "chunk": i})
		entries = append(entries, core.SearchEntry{
			Source:      sourceName,
			SourceID:    row.Path + "#chunk" + itoa(i),
			ContentType: contentType,
			Title:       row.Title,
			Content:     chunk,
			Metadata:    chunkMeta,
			Timestamp:   &ts,
		})
	}
	return entries
}

// relativePath returns path relative to dir, falling back to path on error.
func relativePath(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil { // nocov — both args are always absolute paths from WalkDir
		return path
	}
	return rel
}

// unixToTime converts a Unix timestamp to a time.Time value.
func unixToTime(unix int64) time.Time {
	return time.Unix(unix, 0)
}

// isInsideDir reports whether path is safely within dir (no directory traversal).
func isInsideDir(dir, path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil { // nocov — filepath.Abs only fails on truly broken paths
		return false
	}
	dirAbs, err := filepath.Abs(dir)
	if err != nil { // nocov
		return false
	}
	return strings.HasPrefix(abs, dirAbs+string(os.PathSeparator)) || abs == dirAbs
}

// isPathInConfiguredDirs reports whether path falls inside any of the configured directories.
func isPathInConfiguredDirs(path string, dirs []string) bool {
	for _, d := range dirs {
		if isInsideDir(d, path) {
			return true
		}
	}
	return false
}
