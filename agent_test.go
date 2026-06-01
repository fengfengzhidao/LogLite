package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestAgentListsAllowedLogsAndRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.log"), []byte("INFO ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignore.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	agent, err := newAgentServer("secret", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(agent.routes())
	defer server.Close()

	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/files", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	var result LogDirectoryResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "dir-0/app.log" {
		t.Fatalf("unexpected remote files: %#v", result.Files)
	}

	if _, err := agent.localPath("dir-0/../outside.log"); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}

func TestAgentWebSocketTailPushesNewLines(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	if err := os.WriteFile(logPath, []byte("INFO ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	agent, err := newAgentServer("secret", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(agent.routes())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/tail/stream?token=secret&path=dir-0%2Fapp.log&offset=11&encoding=auto"
	connection, _, err := websocket.Dial(ctx, socketURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("ERROR remote line\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	_, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var update TailUpdateResult
	if err := json.Unmarshal(payload, &update); err != nil {
		t.Fatal(err)
	}
	if len(update.Lines) != 1 || update.Lines[0].Level != "error" {
		t.Fatalf("unexpected tail update: %#v", update)
	}
}
