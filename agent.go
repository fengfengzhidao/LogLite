package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type agentServer struct {
	token   string
	logDirs []string
	service *LogService
}

func runAgent(args []string) error {
	flags := flag.NewFlagSet("agent", flag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:8089", "agent listen address")
	token := flags.String("token", "", "access token")
	logDirs := flags.String("log-dir", "", "allowed log directories, separated by semicolons")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*token) == "" {
		return errors.New("agent token is required: use -token")
	}

	dirs := splitLogDirs(*logDirs)
	if len(dirs) == 0 {
		return errors.New("at least one log directory is required: use -log-dir")
	}

	server, err := newAgentServer(*token, dirs)
	if err != nil {
		return err
	}

	log.Printf("LogLite agent listening on %s", *listen)
	for _, dir := range server.logDirs {
		log.Printf("allow log directory: %s", dir)
	}
	return http.ListenAndServe(*listen, server.routes())
}

func splitLogDirs(value string) []string {
	var dirs []string
	for _, item := range strings.Split(value, ";") {
		if item = strings.TrimSpace(item); item != "" {
			dirs = append(dirs, item)
		}
	}
	return dirs
}

func newAgentServer(token string, dirs []string) (*agentServer, error) {
	server := &agentServer{
		token:   token,
		service: &LogService{},
	}
	for _, dir := range dirs {
		absolute, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return nil, fmt.Errorf("log directory %q: %w", dir, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("log directory %q is not a directory", dir)
		}
		server.logDirs = append(server.logDirs, filepath.Clean(absolute))
	}
	return server, nil
}

func (s *agentServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.withAuth(s.handleHealth))
	mux.HandleFunc("/api/files", s.withAuth(s.handleFiles))
	mux.HandleFunc("/api/tail", s.withAuth(s.handleTail))
	mux.HandleFunc("/api/search", s.withAuth(s.handleSearch))
	mux.HandleFunc("/api/tail/stream", s.handleTailStream)
	return mux
}

func (s *agentServer) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, "token invalid", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *agentServer) authorized(r *http.Request) bool {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	return token != "" && token == s.token
}

func (s *agentServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *agentServer) handleFiles(w http.ResponseWriter, _ *http.Request) {
	result := &LogDirectoryResult{
		Root:     "remote",
		Files:    []LogFile{},
		Warnings: []string{},
	}
	for _, dir := range s.logDirs {
		files, err := s.service.ListLogFiles(dir)
		if err != nil {
			result.Warnings = append(result.Warnings, dir+" 扫描失败："+err.Error())
			continue
		}
		for _, file := range files.Files {
			remotePath, err := s.remotePath(file.Path)
			if err != nil {
				continue
			}
			file.Path = remotePath
			result.Files = append(result.Files, file)
		}
	}
	writeJSON(w, result)
}

func (s *agentServer) handleTail(w http.ResponseWriter, r *http.Request) {
	path, err := s.localPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	result, err := s.service.ReadTail(path, lines, r.URL.Query().Get("encoding"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result.Path = r.URL.Query().Get("path")
	writeJSON(w, result)
}

func (s *agentServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Path    string        `json:"path"`
		Options SearchOptions `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	path, err := s.localPath(request.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := s.service.SearchInFile(path, request.Options)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result.Path = request.Path
	writeJSON(w, result)
}

func (s *agentServer) handleTailStream(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "token invalid", http.StatusUnauthorized)
		return
	}
	path, err := s.localPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer connection.CloseNow()

	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			result, err := s.service.ReadTailUpdate(path, offset, r.URL.Query().Get("encoding"))
			if err != nil {
				writeWebSocketError(r.Context(), connection, err)
				return
			}
			result.Path = r.URL.Query().Get("path")
			offset = result.Size
			if len(result.Lines) == 0 && !result.Rotated && result.Warning == "" {
				continue
			}
			payload, err := json.Marshal(result)
			if err != nil {
				return
			}
			if err := connection.Write(r.Context(), websocket.MessageText, payload); err != nil {
				return
			}
		}
	}
}

func writeWebSocketError(ctx context.Context, connection *websocket.Conn, err error) {
	payload, _ := json.Marshal(map[string]string{"error": err.Error()})
	_ = connection.Write(ctx, websocket.MessageText, payload)
}

func (s *agentServer) localPath(remotePath string) (string, error) {
	remotePath = filepath.ToSlash(strings.TrimSpace(remotePath))
	parts := strings.SplitN(remotePath, "/", 2)
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "dir-") {
		return "", errors.New("日志路径不合法")
	}
	index, err := strconv.Atoi(strings.TrimPrefix(parts[0], "dir-"))
	if err != nil || index < 0 || index >= len(s.logDirs) {
		return "", errors.New("日志路径不合法")
	}
	root := s.logDirs[index]
	localPath := filepath.Clean(filepath.Join(root, filepath.FromSlash(parts[1])))
	relative, err := filepath.Rel(root, localPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("日志路径不在允许目录内")
	}
	if !isLogFile(localPath) {
		return "", errors.New("只支持 .log / .txt 日志文件")
	}
	return localPath, nil
}

func (s *agentServer) remotePath(localPath string) (string, error) {
	localPath = filepath.Clean(localPath)
	for index, root := range s.logDirs {
		relative, err := filepath.Rel(root, localPath)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Sprintf("dir-%d/%s", index, filepath.ToSlash(relative)), nil
		}
	}
	return "", errors.New("日志路径不在允许目录内")
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
