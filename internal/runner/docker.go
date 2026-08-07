package runner

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const dockerAPIVersion = "v1.44"
const runnerUID = 1000

type DockerClient struct {
	http *http.Client
}

func NewDockerClient(socketPath string) *DockerClient {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	}}
	return &DockerClient{http: &http.Client{Transport: transport}}
}

func (d *DockerClient) request(ctx context.Context, method, path string, input any) (*http.Response, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://docker/"+dockerAPIVersion+path, body)
	if err != nil {
		return nil, err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.http.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("Docker %s %s returned %s: %s", method, path, response.Status, strings.TrimSpace(string(message)))
	}
	return response, nil
}

type ContainerLimits struct {
	Image         string
	Network       string
	MemoryBytes   int64
	NanoCPUs      int64
	PIDs          int64
	Timeout       time.Duration
	CodexAuthPath string
	CACertPath    string
}

type ContainerSummary struct {
	ID      string `json:"Id"`
	Created int64  `json:"Created"`
	State   string `json:"State"`
}

func (d *DockerClient) ListRunners(ctx context.Context) ([]ContainerSummary, error) {
	filters, err := json.Marshal(map[string][]string{"label": {"com.mainspring.ephemeral=true"}})
	if err != nil {
		return nil, err
	}
	response, err := d.request(ctx, http.MethodGet, "/containers/json?all=true&filters="+url.QueryEscape(string(filters)), nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var containers []ContainerSummary
	if err := json.NewDecoder(response.Body).Decode(&containers); err != nil {
		return nil, err
	}
	return containers, nil
}

func (d *DockerClient) CreateRunner(ctx context.Context, name string, limits ContainerLimits) (string, error) {
	pids := limits.PIDs
	hostConfig := map[string]any{
		"NetworkMode": limits.Network, "ReadonlyRootfs": true, "Memory": limits.MemoryBytes,
		"NanoCpus": limits.NanoCPUs, "PidsLimit": pids, "CapDrop": []string{"ALL"},
		"SecurityOpt": []string{"no-new-privileges:true"}, "AutoRemove": false,
		"Tmpfs": map[string]string{
			"/tmp":              "rw,noexec,nosuid,size=64m,uid=1000,gid=1000,mode=1777",
			"/home/node/.cache": "rw,noexec,nosuid,size=32m,uid=1000,gid=1000,mode=0700",
		},
	}
	var binds []string
	if limits.CodexAuthPath != "" {
		binds = append(binds, limits.CodexAuthPath+":/home/node/.codex/auth.json:ro")
	}
	if limits.CACertPath != "" {
		binds = append(binds, limits.CACertPath+":/etc/ssl/certs/ca-certificates.crt:ro")
	}
	if len(binds) > 0 {
		hostConfig["Binds"] = binds
	}
	request := map[string]any{
		"Image":      limits.Image,
		"Cmd":        []string{"runner", "/work/invocation.json", "/work/result.json"},
		"WorkingDir": "/work",
		"Volumes": map[string]any{
			"/work":             map[string]any{},
			"/home/node/.codex": map[string]any{},
		},
		"Labels":     map[string]string{"com.mainspring.component": "agent-runner", "com.mainspring.ephemeral": "true"},
		"HostConfig": hostConfig,
	}
	response, err := d.request(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(name), request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var output struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&output); err != nil {
		return "", err
	}
	return output.ID, nil
}

func (d *DockerClient) PutFile(ctx context.Context, containerID, path, name string, content []byte) error {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Uid: runnerUID, Gid: runnerUID, Size: int64(len(content))}); err != nil {
		return err
	}
	if _, err := writer.Write(content); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://docker/"+dockerAPIVersion+"/containers/"+containerID+"/archive?path="+url.QueryEscape(path), bytes.NewReader(archive.Bytes()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-tar")
	response, err := d.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("upload runner input returned %s: %s", response.Status, message)
	}
	return nil
}

func (d *DockerClient) Start(ctx context.Context, containerID string) error {
	response, err := d.request(ctx, http.MethodPost, "/containers/"+containerID+"/start", nil)
	if response != nil {
		response.Body.Close()
	}
	return err
}

func (d *DockerClient) Wait(ctx context.Context, containerID string) (int, error) {
	response, err := d.request(ctx, http.MethodPost, "/containers/"+containerID+"/wait?condition=not-running", nil)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	var output struct {
		StatusCode int `json:"StatusCode"`
	}
	if err := json.NewDecoder(response.Body).Decode(&output); err != nil {
		return 0, err
	}
	return output.StatusCode, nil
}

func (d *DockerClient) ReadFile(ctx context.Context, containerID, path string) ([]byte, error) {
	response, err := d.request(ctx, http.MethodGet, "/containers/"+containerID+"/archive?path="+url.QueryEscape(path), nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	reader := tar.NewReader(response.Body)
	if _, err := reader.Next(); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(reader, 16<<20))
}

func (d *DockerClient) Logs(ctx context.Context, containerID string) string {
	response, err := d.request(ctx, http.MethodGet, "/containers/"+containerID+"/logs?stdout=true&stderr=true&tail=100", nil)
	if err != nil {
		return err.Error()
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	return strings.TrimSpace(string(body))
}

func (d *DockerClient) Stop(ctx context.Context, containerID string) error {
	response, err := d.request(ctx, http.MethodPost, "/containers/"+containerID+"/stop?t=2", nil)
	if response != nil {
		response.Body.Close()
	}
	return err
}

func (d *DockerClient) Remove(ctx context.Context, containerID string) error {
	response, err := d.request(ctx, http.MethodDelete, "/containers/"+containerID+"?force=true&v=true", nil)
	if response != nil {
		response.Body.Close()
	}
	return err
}
