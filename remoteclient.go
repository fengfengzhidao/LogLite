package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type RemoteServer struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Token   string `json:"token"`
}

type remoteClient struct {
	server RemoteServer
	client *http.Client
}

func newRemoteClient(server RemoteServer) (*remoteClient, error) {
	server.Address = strings.TrimRight(strings.TrimSpace(server.Address), "/")
	server.Token = strings.TrimSpace(server.Token)
	if server.Address == "" {
		return nil, errors.New("请输入 agent 地址")
	}
	if _, err := url.ParseRequestURI(server.Address); err != nil {
		return nil, errors.New("agent 地址不合法")
	}
	if server.Token == "" {
		return nil, errors.New("请输入 agent token")
	}
	return &remoteClient{
		server: server,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				Proxy: nil,
			},
		},
	}, nil
}

func (s *LogService) ListRemoteLogFiles(server RemoteServer) (*LogDirectoryResult, error) {
	client, err := newRemoteClient(server)
	if err != nil {
		return nil, err
	}
	var result LogDirectoryResult
	if err := client.get("/api/files", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *LogService) ReadRemoteTail(server RemoteServer, path string, lines int, encoding string) (*LogContentResult, error) {
	client, err := newRemoteClient(server)
	if err != nil {
		return nil, err
	}
	var result LogContentResult
	query := url.Values{
		"path":     []string{path},
		"lines":    []string{fmt.Sprint(lines)},
		"encoding": []string{encoding},
	}
	if err := client.get("/api/tail", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *LogService) SearchRemoteInFile(server RemoteServer, path string, options SearchOptions) (*SearchResult, error) {
	client, err := newRemoteClient(server)
	if err != nil {
		return nil, err
	}
	var result SearchResult
	if err := client.post("/api/search", map[string]any{
		"path":    path,
		"options": options,
	}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *remoteClient) get(path string, query url.Values, target any) error {
	if query == nil {
		query = url.Values{}
	}
	return c.do(http.MethodGet, path+"?"+query.Encode(), nil, target)
}

func (c *remoteClient) post(path string, body any, target any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.do(http.MethodPost, path, bytes.NewReader(payload), target)
}

func (c *remoteClient) do(method string, path string, body io.Reader, target any) error {
	request, err := http.NewRequest(method, c.server.Address+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.server.Token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return errors.New("连接 agent 失败：" + err.Error())
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("agent 返回 %s：%s", response.Status, strings.TrimSpace(string(message)))
	}
	return json.NewDecoder(response.Body).Decode(target)
}
