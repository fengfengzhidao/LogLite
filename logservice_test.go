package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogServiceMVPFlow(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	content := []string{
		"2026-05-21 10:14:01 INFO request started",
		"2026-05-21 10:14:02 WARN retry database connection",
		"2026-05-21 10:14:03 ERROR failed to connect database",
		"2026-05-21 10:14:04 INFO request finished 500",
	}

	if err := os.WriteFile(logPath, []byte(content[0]+"\n"+content[1]+"\n"+content[2]+"\n"+content[3]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignore.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	service := &LogService{}
	files, err := service.ListLogFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Files) != 1 {
		t.Fatalf("expected 1 log file, got %d", len(files.Files))
	}

	droppedFile, err := service.GetLogFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if droppedFile.Name != "app.log" {
		t.Fatalf("expected dropped file metadata, got %q", droppedFile.Name)
	}

	tail, err := service.ReadTail(logPath, 2, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if len(tail.Lines) != 2 {
		t.Fatalf("expected 2 tail lines, got %d", len(tail.Lines))
	}
	if tail.Lines[0].Level != "error" {
		t.Fatalf("expected first tail line to be error, got %q", tail.Lines[0].Level)
	}

	search, err := service.SearchInFile(logPath, SearchOptions{
		Keyword:      "database",
		ContextLines: 1,
		Limit:        10,
		Encoding:     "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	if search.HitCount != 2 {
		t.Fatalf("expected 2 hits, got %d", search.HitCount)
	}
	if len(search.Hits) != 1 {
		t.Fatalf("expected adjacent hits to be merged into 1 block, got %d", len(search.Hits))
	}
	if len(search.Hits[0].MatchLines) != 2 {
		t.Fatalf("expected merged block to keep 2 match lines, got %d", len(search.Hits[0].MatchLines))
	}

	multi, err := service.SearchInFiles(files.Files, SearchOptions{
		Keyword:      "ERROR",
		ContextLines: 1,
		Limit:        10,
		Encoding:     "auto",
		StartTime:    "2026-05-21 10:14:03",
		EndTime:      "2026-05-21 10:14:04",
	})
	if err != nil {
		t.Fatal(err)
	}
	if multi.HitCount != 1 {
		t.Fatalf("expected 1 filtered hit, got %d", multi.HitCount)
	}
}

func TestCleanANSIControlSequences(t *testing.T) {
	line := newLogLine(1, "\x1b[36m[info]\x1b[0m main.go:12 hello\x00")
	if line.Text != "[info] main.go:12 hello" {
		t.Fatalf("unexpected cleaned text: %q", line.Text)
	}
	if line.Level != "info" {
		t.Fatalf("expected info level, got %q", line.Level)
	}
}

func TestDateOnlyTimeFilter(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	content := strings.Join([]string{
		"2025-03-08 INFO old line",
		"2025-03-09 INFO target line",
		"2025-03-10 INFO end day line",
		"2025-03-11 INFO future line",
	}, "\n")

	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	service := &LogService{}
	result, err := service.SearchInFile(logPath, SearchOptions{
		StartTime: "2025-03-09",
		EndTime:   "2025-03-10",
		Limit:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.HitCount != 2 {
		t.Fatalf("expected 2 hits in date range, got %d", result.HitCount)
	}
}
