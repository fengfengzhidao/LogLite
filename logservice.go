package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const (
	defaultTailLines     = 2000
	defaultSearchHits    = 200
	maxTailReadBytes     = 4 * 1024 * 1024
	maxScannerBytes      = 1024 * 1024
	maxMultiSearchFiles  = 200
	defaultTailPollBytes = 2 * 1024 * 1024
)

var logExts = map[string]bool{
	".log": true,
	".txt": true,
}

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

type LogService struct{}

type LogFile struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

type LogDirectoryResult struct {
	Root     string    `json:"root"`
	Files    []LogFile `json:"files"`
	Warnings []string  `json:"warnings"`
}

type LogLine struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
	Level  string `json:"level"`
}

type LogContentResult struct {
	Path      string    `json:"path"`
	Lines     []LogLine `json:"lines"`
	TotalRead int       `json:"totalRead"`
	Truncated bool      `json:"truncated"`
	Warning   string    `json:"warning"`
	Size      int64     `json:"size"`
}

type SearchHit struct {
	LineNumber int       `json:"lineNumber"`
	MatchLines []int     `json:"matchLines"`
	Lines      []LogLine `json:"lines"`
}

type SearchOptions struct {
	Keyword       string `json:"keyword"`
	ContextLines  int    `json:"contextLines"`
	Limit         int    `json:"limit"`
	CaseSensitive bool   `json:"caseSensitive"`
	UseRegex      bool   `json:"useRegex"`
	Encoding      string `json:"encoding"`
	StartTime     string `json:"startTime"`
	EndTime       string `json:"endTime"`
}

type SearchResult struct {
	Path     string      `json:"path"`
	Keyword  string      `json:"keyword"`
	Hits     []SearchHit `json:"hits"`
	HitCount int         `json:"hitCount"`
	Limited  bool        `json:"limited"`
	Scanned  int         `json:"scanned"`
	Warning  string      `json:"warning"`
}

type FileSearchResult struct {
	File     LogFile     `json:"file"`
	Keyword  string      `json:"keyword"`
	Hits     []SearchHit `json:"hits"`
	HitCount int         `json:"hitCount"`
	Limited  bool        `json:"limited"`
	Scanned  int         `json:"scanned"`
	Warning  string      `json:"warning"`
}

type MultiSearchResult struct {
	Keyword      string             `json:"keyword"`
	Files        []FileSearchResult `json:"files"`
	FilesScanned int                `json:"filesScanned"`
	HitCount     int                `json:"hitCount"`
	Limited      bool               `json:"limited"`
	Warnings     []string           `json:"warnings"`
}

type TailUpdateResult struct {
	Path      string    `json:"path"`
	Lines     []LogLine `json:"lines"`
	Size      int64     `json:"size"`
	Truncated bool      `json:"truncated"`
	Rotated   bool      `json:"rotated"`
	Warning   string    `json:"warning"`
}

func (s *LogService) ListLogFiles(dir string) (*LogDirectoryResult, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("请输入日志目录")
	}

	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("请选择一个目录")
	}

	result := &LogDirectoryResult{
		Root:     dir,
		Files:    []LogFile{},
		Warnings: []string{},
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !isLogFile(entry.Name()) {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		fileInfo, err := entry.Info()
		if err != nil {
			result.Warnings = append(result.Warnings, entry.Name()+" 读取文件信息失败")
			continue
		}

		result.Files = append(result.Files, fileInfoToLogFile(path, fileInfo))
	}

	sort.Slice(result.Files, func(i, j int) bool {
		return result.Files[i].ModTime > result.Files[j].ModTime
	})

	return result, nil
}

func (s *LogService) ReadTail(path string, lines int, encoding string) (*LogContentResult, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("请选择日志文件")
	}
	if lines <= 0 {
		lines = defaultTailLines
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("请选择日志文件")
	}

	readSize := info.Size()
	truncated := false
	if readSize > maxTailReadBytes {
		readSize = maxTailReadBytes
		truncated = true
	}

	offset := info.Size() - readSize
	buffer := make([]byte, readSize)
	if readSize > 0 {
		if _, err := file.ReadAt(buffer, offset); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
	}

	parts, warning := decodeAndSplitLines(buffer, encoding)
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}

	resultLines := make([]LogLine, 0, len(parts))
	for index, line := range parts {
		resultLines = append(resultLines, newLogLine(index+1, line))
	}

	result := &LogContentResult{
		Path:      path,
		Lines:     resultLines,
		TotalRead: len(resultLines),
		Truncated: truncated,
		Warning:   warning,
		Size:      info.Size(),
	}
	if truncated {
		result.Warning = joinWarnings(result.Warning, "文件较大，当前只读取尾部内容")
	}

	return result, nil
}

func (s *LogService) ReadTailUpdate(path string, offset int64, encoding string) (*TailUpdateResult, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("请选择日志文件")
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("请选择日志文件")
	}

	result := &TailUpdateResult{Path: path, Size: info.Size()}
	if info.Size() < offset {
		offset = 0
		result.Rotated = true
	}
	if info.Size() == offset {
		return result, nil
	}

	readSize := info.Size() - offset
	if readSize > defaultTailPollBytes {
		offset = info.Size() - defaultTailPollBytes
		readSize = defaultTailPollBytes
		result.Truncated = true
		result.Warning = "新增内容较多，当前只追加最近一段"
	}

	buffer := make([]byte, readSize)
	if _, err := file.ReadAt(buffer, offset); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	parts, warning := decodeAndSplitLines(buffer, encoding)
	result.Warning = joinWarnings(result.Warning, warning)
	result.Lines = make([]LogLine, 0, len(parts))
	for index, line := range parts {
		result.Lines = append(result.Lines, newLogLine(index+1, line))
	}

	return result, nil
}

func (s *LogService) GetLogFile(path string) (*LogFile, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("请选择日志文件")
	}
	if !isLogFile(path) {
		return nil, errors.New("只支持 .log / .txt 日志文件")
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("请拖入日志文件，不是目录")
	}

	file := fileInfoToLogFile(path, info)
	return &file, nil
}

func (s *LogService) SearchInFile(path string, options SearchOptions) (*SearchResult, error) {
	file, err := s.GetLogFile(path)
	if err != nil {
		return nil, err
	}

	result, err := searchFile(file, normalizeSearchOptions(options))
	if err != nil {
		return nil, err
	}

	return &SearchResult{
		Path:     file.Path,
		Keyword:  result.Keyword,
		Hits:     result.Hits,
		HitCount: result.HitCount,
		Limited:  result.Limited,
		Scanned:  result.Scanned,
		Warning:  result.Warning,
	}, nil
}

func (s *LogService) SearchInFiles(files []LogFile, options SearchOptions) (*MultiSearchResult, error) {
	options = normalizeSearchOptions(options)
	if strings.TrimSpace(options.Keyword) == "" && strings.TrimSpace(options.StartTime) == "" && strings.TrimSpace(options.EndTime) == "" {
		return nil, errors.New("请输入搜索关键字或时间范围")
	}

	result := &MultiSearchResult{
		Keyword:  options.Keyword,
		Files:    []FileSearchResult{},
		Warnings: []string{},
	}

	if len(files) > maxMultiSearchFiles {
		files = files[:maxMultiSearchFiles]
		result.Limited = true
		result.Warnings = append(result.Warnings, "文件数量较多，当前只搜索前 200 个文件")
	}

	for _, file := range files {
		if !isLogFile(file.Path) {
			continue
		}

		item, err := searchFile(&file, options)
		if err != nil {
			result.Warnings = append(result.Warnings, file.Name+" 搜索失败："+err.Error())
			continue
		}
		result.FilesScanned++
		result.HitCount += item.HitCount
		if item.Limited {
			result.Limited = true
		}
		if item.HitCount > 0 {
			result.Files = append(result.Files, *item)
		}
	}

	sort.Slice(result.Files, func(i, j int) bool {
		if result.Files[i].HitCount == result.Files[j].HitCount {
			return result.Files[i].File.Name < result.Files[j].File.Name
		}
		return result.Files[i].HitCount > result.Files[j].HitCount
	})

	return result, nil
}

func searchFile(file *LogFile, options SearchOptions) (*FileSearchResult, error) {
	matcher, err := newMatcher(options)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(file.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := readerForEncoding(f, options.Encoding)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxScannerBytes)

	startTime, hasStart := parseFilterTime(options.StartTime, false)
	endTime, hasEnd := parseFilterTime(options.EndTime, true)
	lineNumber := 0
	hitCount := 0
	limited := false
	before := make([]LogLine, 0, options.ContextLines)
	pendingAfter := 0
	var currentHit *SearchHit
	hits := make([]SearchHit, 0)

	for scanner.Scan() {
		lineNumber++
		line := newLogLine(lineNumber, scanner.Text())
		inTimeRange := lineInTimeRange(line.Text, startTime, hasStart, endTime, hasEnd)
		matched := inTimeRange && matcher(line.Text)

		if matched {
			hitCount++
			if len(hits) >= options.Limit {
				limited = true
				continue
			}

			hit := SearchHit{
				LineNumber: line.Number,
				MatchLines: []int{line.Number},
				Lines:      append([]LogLine{}, before...),
			}
			hit.Lines = append(hit.Lines, line)
			hits = append(hits, hit)
			currentHit = &hits[len(hits)-1]
			pendingAfter = options.ContextLines
		} else if pendingAfter > 0 && currentHit != nil {
			currentHit.Lines = append(currentHit.Lines, line)
			pendingAfter--
		}

		before = append(before, line)
		if len(before) > options.ContextLines {
			before = before[1:]
		}
	}

	result := &FileSearchResult{
		File:     *file,
		Keyword:  options.Keyword,
		Hits:     hits,
		HitCount: hitCount,
		Limited:  limited,
		Scanned:  lineNumber,
	}

	if err := scanner.Err(); err != nil {
		result.Warning = "搜索过程中有日志行过长或读取失败，结果可能不完整"
	}
	result.Hits = mergeSearchHits(result.Hits)

	return result, nil
}

func mergeSearchHits(hits []SearchHit) []SearchHit {
	if len(hits) <= 1 {
		return hits
	}

	merged := make([]SearchHit, 0, len(hits))
	for _, hit := range hits {
		if len(merged) == 0 {
			merged = append(merged, hit)
			continue
		}

		last := &merged[len(merged)-1]
		lastEnd := last.Lines[len(last.Lines)-1].Number
		hitStart := hit.Lines[0].Number
		if hitStart > lastEnd+1 {
			merged = append(merged, hit)
			continue
		}

		last.MatchLines = append(last.MatchLines, hit.MatchLines...)
		existing := map[int]bool{}
		for _, line := range last.Lines {
			existing[line.Number] = true
		}
		for _, line := range hit.Lines {
			if !existing[line.Number] {
				last.Lines = append(last.Lines, line)
			}
		}
	}

	return merged
}

func normalizeSearchOptions(options SearchOptions) SearchOptions {
	options.Keyword = strings.TrimSpace(options.Keyword)
	if options.ContextLines < 0 {
		options.ContextLines = 2
	}
	if options.Limit <= 0 {
		options.Limit = defaultSearchHits
	}
	if options.Encoding == "" {
		options.Encoding = "auto"
	}
	return options
}

func newMatcher(options SearchOptions) (func(string) bool, error) {
	if strings.TrimSpace(options.Keyword) == "" {
		return func(string) bool {
			return true
		}, nil
	}

	if options.UseRegex {
		pattern := options.Keyword
		if !options.CaseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, errors.New("正则表达式不合法：" + err.Error())
		}
		return re.MatchString, nil
	}

	needle := options.Keyword
	if !options.CaseSensitive {
		needle = strings.ToLower(needle)
	}
	return func(text string) bool {
		if options.CaseSensitive {
			return strings.Contains(text, needle)
		}
		return strings.Contains(strings.ToLower(text), needle)
	}, nil
}

func fileInfoToLogFile(path string, info os.FileInfo) LogFile {
	return LogFile{
		Name:    filepath.Base(path),
		Path:    path,
		Size:    info.Size(),
		ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
	}
}

func isLogFile(name string) bool {
	return logExts[strings.ToLower(filepath.Ext(name))]
}

func detectLevel(text string) string {
	upper := strings.ToUpper(text)
	switch {
	case strings.Contains(upper, "ERROR") || strings.Contains(upper, "FATAL"):
		return "error"
	case strings.Contains(upper, "WARN"):
		return "warn"
	case strings.Contains(upper, "INFO"):
		return "info"
	case strings.Contains(upper, "DEBUG"):
		return "debug"
	default:
		return "normal"
	}
}

func newLogLine(number int, text string) LogLine {
	text = cleanLogText(text)
	return LogLine{
		Number: number,
		Text:   text,
		Level:  detectLevel(text),
	}
}

func cleanLogText(text string) string {
	text = ansiEscapePattern.ReplaceAllString(text, "")
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return r
		case r < 32 || r == 127:
			return -1
		default:
			return r
		}
	}, text)
	return text
}

func readerForEncoding(reader io.Reader, encoding string) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "gbk":
		return transform.NewReader(reader, simplifiedchinese.GBK.NewDecoder())
	default:
		return reader
	}
}

func decodeAndSplitLines(data []byte, encoding string) ([]string, string) {
	var text string
	var warning string

	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "gbk":
		decoded, err := io.ReadAll(transform.NewReader(bytes.NewReader(data), simplifiedchinese.GBK.NewDecoder()))
		if err != nil {
			warning = "GBK 解码有异常，结果可能不完整"
		}
		text = string(decoded)
	case "utf-8", "utf8":
		text = string(data)
	default:
		if utf8.Valid(data) {
			text = string(data)
		} else {
			decoded, err := io.ReadAll(transform.NewReader(bytes.NewReader(data), simplifiedchinese.GBK.NewDecoder()))
			if err != nil {
				warning = "自动识别编码失败，结果可能有乱码"
				text = string(data)
			} else {
				text = string(decoded)
			}
		}
	}

	text = strings.ReplaceAll(text, "\r\n", "\n")
	parts := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(parts) == 1 && parts[0] == "" {
		return []string{}, warning
	}
	return parts, warning
}

func parseFilterTime(value string, isEnd bool) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}

	if parsed, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		if isEnd {
			return parsed.Add(24*time.Hour - time.Nanosecond), true
		}
		return parsed, true
	}

	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		time.RFC3339,
	}
	for _, layout := range layouts {
		parsed, err := time.ParseInLocation(layout, value, time.Local)
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func lineInTimeRange(text string, start time.Time, hasStart bool, end time.Time, hasEnd bool) bool {
	if !hasStart && !hasEnd {
		return true
	}

	lineTime, ok := parseTimeFromLine(text)
	if !ok {
		return true
	}
	if hasStart && lineTime.Before(start) {
		return false
	}
	if hasEnd && lineTime.After(end) {
		return false
	}
	return true
}

func parseTimeFromLine(text string) (time.Time, bool) {
	patterns := []struct {
		re     *regexp.Regexp
		layout string
	}{
		{regexp.MustCompile(`\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}`), "2006-01-02 15:04:05"},
		{regexp.MustCompile(`\d{4}/\d{2}/\d{2}[ T]\d{2}:\d{2}:\d{2}`), "2006/01/02 15:04:05"},
		{regexp.MustCompile(`\d{4}-\d{2}-\d{2}`), "2006-01-02"},
		{regexp.MustCompile(`\d{4}/\d{2}/\d{2}`), "2006/01/02"},
	}

	for _, pattern := range patterns {
		raw := pattern.re.FindString(text)
		if raw == "" {
			continue
		}
		raw = strings.Replace(raw, "T", " ", 1)
		parsed, err := time.ParseInLocation(pattern.layout, raw, time.Local)
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func joinWarnings(items ...string) string {
	var values []string
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			values = append(values, item)
		}
	}
	return strings.Join(values, "；")
}
