// Package dockerengine adapts the stage-only runner launcher to the Docker
// Engine API. Only the launcher process receives access to this adapter.
package dockerengine

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	defaultSocketPath       = "/var/run/docker.sock"
	defaultAPIVersion       = "v1.45"
	maximumResponseBytes    = 1 << 20
	maximumBrokerCABytes    = 64 << 10
	runnerIdentityDirectory = "/var/run/secrets/spyglass.io/runner-identity"
	runnerIdentityTokenFile = runnerIdentityDirectory + "/token"
	runnerBrokerCADirectory = "/var/run/secrets/spyglass.io/broker-ca"
	runnerBrokerCAFile      = runnerBrokerCADirectory + "/ca.crt"
	managedLabel            = "spyglass.io/docker-runner-managed"
	invocationLabel         = "spyglass.io/invocation-id"
	profileLabel            = "spyglass.io/profile"
	contractLabel           = "spyglass.io/launch-contract-sha256"
	tokenDigestLabel        = "spyglass.io/identity-token-sha256"
	deadlineLabel           = "spyglass.io/active-deadline-unix"
)

var (
	digestImage = regexp.MustCompile(`^\S+@sha256:[0-9a-f]{64}$`)
	localImage  = regexp.MustCompile(`^[a-z0-9][a-z0-9./_-]{0,199}:local$`)
	dockerName  = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)
	profileName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,49}$`)
	apiVersion  = regexp.MustCompile(`^v[0-9]+\.[0-9]+$`)
)

type ResourceProfile struct {
	NanoCPUs       int64
	MemoryBytes    int64
	PidsLimit      int64
	WorkTmpfsBytes int64
}

type Config struct {
	SocketPath        string
	Endpoint          string
	APIVersion        string
	RunnerImage       string
	Network           string
	BrokerURL         string
	IdentityDirectory string
	BrokerCAFile      string
	HTTPClient        *http.Client
	AllowLocalImage   bool
	// AllowPermissionlessIdentityFiles exists only for the local-secure gate
	// running on a Windows-backed bind mount. Stage must retain Unix mode checks.
	AllowPermissionlessIdentityFiles bool
	Profiles                         map[string]ResourceProfile
	ActiveDeadline                   time.Duration
	Retention                        time.Duration
}

type RunnerContainers struct {
	origin, version, image, network, brokerURL, identityDirectory, brokerCAFile string
	http                                                                        *http.Client
	profiles                                                                    map[string]ResourceProfile
	activeDeadline, retention                                                   time.Duration
	allowPermissionlessIdentityFiles                                            bool
}

func New(config Config) (*RunnerContainers, error) {
	if config.APIVersion == "" {
		config.APIVersion = defaultAPIVersion
	}
	validImage := digestImage.MatchString(config.RunnerImage) || (config.AllowLocalImage && localImage.MatchString(config.RunnerImage))
	if !apiVersion.MatchString(config.APIVersion) || !validImage || len(config.RunnerImage) > 500 || !dockerName.MatchString(config.Network) {
		return nil, errors.New("Docker runner API version, image digest, or network is invalid")
	}
	broker, err := url.Parse(strings.TrimSpace(config.BrokerURL))
	if err != nil || broker.Scheme != "https" || broker.Host == "" || broker.User != nil || broker.RawQuery != "" || broker.Fragment != "" {
		return nil, errors.New("Docker runner broker URL must be an exact HTTPS URL")
	}
	identityDirectory, err := filepath.Abs(config.IdentityDirectory)
	if err != nil || !filepath.IsAbs(config.IdentityDirectory) || identityDirectory == "/" {
		return nil, errors.New("Docker runner identity directory must be an absolute bounded path")
	}
	brokerCAFile, err := filepath.Abs(config.BrokerCAFile)
	if err != nil || !filepath.IsAbs(config.BrokerCAFile) || brokerCAFile == "/" {
		return nil, errors.New("Docker runner broker CA file must be an absolute path")
	}
	if config.ActiveDeadline < 30*time.Second || config.ActiveDeadline > 24*time.Hour || config.ActiveDeadline%time.Second != 0 || config.Retention < time.Minute || config.Retention > 7*24*time.Hour || config.Retention%time.Second != 0 {
		return nil, errors.New("Docker runner deadline or retention is out of bounds")
	}
	if len(config.Profiles) == 0 || len(config.Profiles) > 20 {
		return nil, errors.New("Docker runner resource profiles are required")
	}
	profiles := make(map[string]ResourceProfile, len(config.Profiles))
	for name, profile := range config.Profiles {
		if !profileName.MatchString(name) || profile.NanoCPUs < 100_000_000 || profile.NanoCPUs > 8_000_000_000 || profile.MemoryBytes < 128<<20 || profile.MemoryBytes > 16<<30 || profile.PidsLimit < 16 || profile.PidsLimit > 1024 || profile.WorkTmpfsBytes < 64<<20 || profile.WorkTmpfsBytes > 16<<30 {
			return nil, fmt.Errorf("Docker runner resource profile %q is invalid", name)
		}
		profiles[name] = profile
	}

	origin := strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	client := config.HTTPClient
	if client == nil {
		socket := config.SocketPath
		if socket == "" {
			socket = defaultSocketPath
		}
		if !filepath.IsAbs(socket) || socket == "/" {
			return nil, errors.New("Docker Engine socket path is invalid")
		}
		dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		}
		client = &http.Client{Transport: transport, Timeout: 15 * time.Second}
		origin = "http://docker"
	} else {
		parsed, parseErr := url.Parse(origin)
		if parseErr != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return nil, errors.New("test Docker Engine endpoint is invalid")
		}
	}
	return &RunnerContainers{origin: origin, version: config.APIVersion, image: config.RunnerImage, network: config.Network, brokerURL: broker.String(), identityDirectory: identityDirectory, brokerCAFile: brokerCAFile, http: client, profiles: profiles, activeDeadline: config.ActiveDeadline, retention: config.Retention, allowPermissionlessIdentityFiles: config.AllowPermissionlessIdentityFiles}, nil
}

func (d *RunnerContainers) Ready(ctx context.Context) error {
	response, err := d.request(ctx, http.MethodGet, "/_ping", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Docker Engine ping returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (d *RunnerContainers) Ensure(ctx context.Context, invocation runnercontrol.Invocation) (string, error) {
	profile, contract, name, err := d.invocationContract(invocation)
	if err != nil {
		return "", err
	}
	existing, found, err := d.inspect(ctx, name)
	if err != nil {
		return "", err
	}
	if found {
		if !matches(existing, invocation, contract, name) {
			return name, fmt.Errorf("%w: existing Docker runner conflicts with the invocation contract", runnercontrol.ErrLaunchUncertain)
		}
		if !d.persistedIdentityMatches(invocation.ID, existing.Config.Labels[tokenDigestLabel]) {
			return name, fmt.Errorf("%w: existing Docker runner identity material is unavailable", runnercontrol.ErrLaunchUncertain)
		}
		if existing.State.Status == "created" {
			if err := d.start(ctx, existing.ID, name); err != nil {
				return name, err
			}
		}
		return name, nil
	}

	tokenDigest, err := d.prepareIdentity(invocation.ID)
	if err != nil {
		return "", err
	}
	deadline := time.Now().UTC().Add(d.activeDeadline).Unix()
	payload := d.createPayload(invocation, profile, contract, tokenDigest, deadline)
	response, err := d.request(ctx, http.MethodPost, d.apiPath("/containers/create")+"?name="+url.QueryEscape(name), payload)
	if err != nil {
		return uncertain(name, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		return uncertain(name, errors.New("Docker runner appeared during create"))
	}
	if response.StatusCode != http.StatusCreated {
		responseErr := engineError("create Docker runner", response)
		if response.StatusCode >= http.StatusInternalServerError {
			return uncertain(name, responseErr)
		}
		d.removeIdentity(invocation.ID)
		return "", responseErr
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := decode(response.Body, &created); err != nil || created.ID == "" {
		return uncertain(name, errors.New("Docker Engine returned an invalid created container identity"))
	}
	if err := d.start(ctx, created.ID, name); err != nil {
		return name, err
	}
	return name, nil
}

func (d *RunnerContainers) Cancel(ctx context.Context, invocation runnercontrol.Invocation) error {
	_, contract, name, err := d.invocationContract(invocation)
	if err != nil || invocation.State != "canceling" || invocation.JobName != name {
		return errors.New("canceling Docker runner identity is invalid")
	}
	container, found, err := d.inspect(ctx, name)
	if err != nil {
		return err
	}
	if !found {
		d.removeIdentity(invocation.ID)
		return nil
	}
	if !matches(container, invocation, contract, name) {
		return errors.New("canceling Docker runner does not match the launch contract")
	}
	if container.State.Running {
		response, requestErr := d.request(ctx, http.MethodPost, d.apiPath("/containers/")+url.PathEscape(container.ID)+"/stop?t=10", nil)
		if requestErr != nil {
			return requestErr
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotModified && response.StatusCode != http.StatusNotFound {
			return engineError("stop Docker runner", response)
		}
	}
	response, err := d.request(ctx, http.MethodDelete, d.apiPath("/containers/")+url.PathEscape(container.ID)+"?force=1&v=1", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
		return engineError("remove Docker runner", response)
	}
	d.removeIdentity(invocation.ID)
	return nil
}

func (d *RunnerContainers) Inspect(ctx context.Context, invocation runnercontrol.Invocation) (runnercontrol.TerminalStatus, error) {
	_, contract, name, err := d.invocationContract(invocation)
	if err != nil || (invocation.State != "launch_uncertain" && invocation.State != "launched" && invocation.State != "canceling") || invocation.JobName != name {
		return runnercontrol.TerminalStatus{}, errors.New("Docker runner identity is invalid")
	}
	container, found, err := d.inspect(ctx, name)
	if err != nil {
		return runnercontrol.TerminalStatus{}, err
	}
	if !found {
		if invocation.State == "canceling" {
			return runnercontrol.TerminalStatus{Terminal: true, Outcome: "canceled"}, nil
		}
		if invocation.State == "launch_uncertain" {
			return runnercontrol.TerminalStatus{}, nil
		}
		return runnercontrol.TerminalStatus{}, errors.New("launched Docker runner was not found")
	}
	if !matches(container, invocation, contract, name) {
		return runnercontrol.TerminalStatus{}, errors.New("Docker runner status does not match the launch contract")
	}
	if invocation.State == "canceling" {
		return runnercontrol.TerminalStatus{Observed: true}, nil
	}
	switch container.State.Status {
	case "created":
		if err := d.start(ctx, container.ID, name); err != nil {
			return runnercontrol.TerminalStatus{}, err
		}
		return runnercontrol.TerminalStatus{Observed: true}, nil
	case "running", "restarting", "paused":
		return runnercontrol.TerminalStatus{Observed: true}, nil
	case "exited":
		outcome := "execution_failed"
		if container.State.ExitCode == 0 {
			outcome = "completed"
		}
		return runnercontrol.TerminalStatus{Observed: true, Terminal: true, Outcome: outcome}, nil
	case "dead":
		return runnercontrol.TerminalStatus{Observed: true, Terminal: true, Outcome: "execution_failed"}, nil
	default:
		return runnercontrol.TerminalStatus{}, errors.New("Docker runner returned an unknown state")
	}
}

func (d *RunnerContainers) Verify(ctx context.Context, token, invocationID string) (runnerbroker.Identity, error) {
	if ids.Validate(invocationID) != nil || token == "" || len(token) > 16<<10 || strings.TrimSpace(token) != token || strings.ContainsAny(token, " \t\r\n\x00") {
		return runnerbroker.Identity{}, runnerbroker.ErrIdentityDenied
	}
	name := containerName(invocationID)
	container, found, err := d.inspect(ctx, name)
	if err != nil {
		return runnerbroker.Identity{}, err
	}
	if !found || !container.State.Running || container.Name != "/"+name || container.Config.Labels[managedLabel] != "true" || container.Config.Labels[invocationLabel] != invocationID || !profileName.MatchString(container.Config.Labels[profileLabel]) || !isHexDigest(container.Config.Labels[contractLabel]) {
		return runnerbroker.Identity{}, runnerbroker.ErrIdentityDenied
	}
	digest := sha256.Sum256([]byte(token))
	expected, decodeErr := hex.DecodeString(container.Config.Labels[tokenDigestLabel])
	if decodeErr != nil || len(expected) != sha256.Size || subtle.ConstantTimeCompare(digest[:], expected) != 1 {
		return runnerbroker.Identity{}, runnerbroker.ErrIdentityDenied
	}
	return runnerbroker.Identity{InvocationID: invocationID, Profile: container.Config.Labels[profileLabel], JobName: name, JobUID: container.ID, PodName: name, PodUID: container.ID}, nil
}

// CleanupExpired removes a bounded set of launcher-managed containers whose
// immutable active deadline has passed. Labels and identity directories make
// the operation restart-safe and independent of in-memory state.
func (d *RunnerContainers) CleanupExpired(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		return 0, errors.New("Docker runner cleanup limit is invalid")
	}
	filters, _ := json.Marshal(map[string][]string{"label": {managedLabel + "=true"}})
	response, err := d.request(ctx, http.MethodGet, d.apiPath("/containers/json")+"?all=1&filters="+url.QueryEscape(string(filters)), nil)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, engineError("list Docker runners", response)
	}
	var containers []struct {
		ID     string            `json:"Id"`
		Labels map[string]string `json:"Labels"`
	}
	if err := decode(response.Body, &containers); err != nil {
		return 0, err
	}
	removed := 0
	var failures []error
	for _, container := range containers {
		if removed >= limit {
			break
		}
		deadline, parseErr := strconv.ParseInt(container.Labels[deadlineLabel], 10, 64)
		invocationID := container.Labels[invocationLabel]
		if parseErr != nil || ids.Validate(invocationID) != nil || now.Unix() < deadline {
			continue
		}
		response, requestErr := d.request(ctx, http.MethodDelete, d.apiPath("/containers/")+url.PathEscape(container.ID)+"?force=1&v=1", nil)
		if requestErr != nil {
			failures = append(failures, requestErr)
			continue
		}
		status := response.StatusCode
		response.Body.Close()
		if status != http.StatusNoContent && status != http.StatusNotFound {
			failures = append(failures, fmt.Errorf("remove expired Docker runner returned HTTP %d", status))
			continue
		}
		d.removeIdentity(invocationID)
		removed++
	}
	return removed, errors.Join(failures...)
}

func (d *RunnerContainers) invocationContract(invocation runnercontrol.Invocation) (ResourceProfile, string, string, error) {
	if ids.Validate(invocation.ID) != nil || ids.Validate(string(invocation.AccountID)) != nil || !profileName.MatchString(invocation.Profile) {
		return ResourceProfile{}, "", "", runnercontrol.ErrInvalidInvocation
	}
	profile, ok := d.profiles[invocation.Profile]
	if !ok {
		return ResourceProfile{}, "", "", fmt.Errorf("Docker runner profile %q is not deployed", invocation.Profile)
	}
	contractInput := struct {
		InvocationID, Profile, Image, Network, BrokerURL string
		ProfilePolicy                                    ResourceProfile
		DeadlineSeconds, RetentionSeconds                int64
	}{invocation.ID, invocation.Profile, d.image, d.network, d.brokerURL, profile, int64(d.activeDeadline / time.Second), int64(d.retention / time.Second)}
	raw, err := json.Marshal(contractInput)
	if err != nil {
		return ResourceProfile{}, "", "", err
	}
	digest := sha256.Sum256(raw)
	return profile, hex.EncodeToString(digest[:]), containerName(invocation.ID), nil
}

func (d *RunnerContainers) createPayload(invocation runnercontrol.Invocation, profile ResourceProfile, contract, tokenDigest string, deadline int64) map[string]any {
	identityHost := filepath.Join(d.identityDirectory, invocation.ID)
	labels := map[string]string{managedLabel: "true", invocationLabel: invocation.ID, profileLabel: invocation.Profile, contractLabel: contract, tokenDigestLabel: tokenDigest, deadlineLabel: strconv.FormatInt(deadline, 10)}
	return map[string]any{
		"Image": d.image, "User": "65532:65532", "WorkingDir": "/work", "Labels": labels,
		"Env": []string{}, "AttachStdin": false, "AttachStdout": false, "AttachStderr": false, "OpenStdin": false, "StdinOnce": false,
		"Cmd": []string{"runner-invocation", "--broker-url=" + d.brokerURL, "--invocation-id=" + invocation.ID, "--identity-token-file=" + runnerIdentityTokenFile, "--broker-ca-file=" + runnerBrokerCAFile},
		"HostConfig": map[string]any{
			"ReadonlyRootfs": true, "NetworkMode": d.network, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"},
			"Memory": profile.MemoryBytes, "NanoCpus": profile.NanoCPUs, "PidsLimit": profile.PidsLimit, "Init": true,
			"AutoRemove": false, "RestartPolicy": map[string]any{"Name": "no", "MaximumRetryCount": 0},
			"Binds": []string{filepath.Join(identityHost, "identity") + ":" + runnerIdentityDirectory + ":ro", filepath.Join(identityHost, "broker") + ":" + runnerBrokerCADirectory + ":ro"},
			"Tmpfs": map[string]string{"/tmp": "rw,noexec,nosuid,nodev,size=64m,uid=65532,gid=65532,mode=1770", "/work": "rw,noexec,nosuid,nodev,size=" + strconv.FormatInt(profile.WorkTmpfsBytes, 10) + ",uid=65532,gid=65532,mode=1770"},
		},
	}
}

func (d *RunnerContainers) prepareIdentity(invocationID string) (string, error) {
	ca, err := os.ReadFile(d.brokerCAFile)
	if err != nil || len(ca) == 0 || len(ca) > maximumBrokerCABytes {
		return "", errors.New("read bounded Docker runner broker CA")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return "", errors.New("Docker runner broker CA contains no certificates")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	directory := filepath.Join(d.identityDirectory, invocationID)
	identityDirectory := filepath.Join(directory, "identity")
	brokerDirectory := filepath.Join(directory, "broker")
	if err := os.MkdirAll(identityDirectory, 0o700); err != nil {
		return "", err
	}
	if err := os.MkdirAll(brokerDirectory, 0o700); err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(identityDirectory, "token"), []byte(token), 0o444, d.allowPermissionlessIdentityFiles); err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(brokerDirectory, "ca.crt"), ca, 0o444, d.allowPermissionlessIdentityFiles); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:]), nil
}

func writeAtomic(path string, contents []byte, mode os.FileMode, allowPermissionless bool) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, contents, mode); err != nil {
		return err
	}
	if err := os.Chmod(temporary, mode); err != nil && !allowPermissionless {
		_ = os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, path)
}

func (d *RunnerContainers) removeIdentity(invocationID string) {
	if ids.Validate(invocationID) == nil {
		_ = os.RemoveAll(filepath.Join(d.identityDirectory, invocationID))
	}
}

func (d *RunnerContainers) persistedIdentityMatches(invocationID, expectedHex string) bool {
	token, err := os.ReadFile(filepath.Join(d.identityDirectory, invocationID, "identity", "token"))
	if err != nil || len(token) == 0 || len(token) > 16<<10 {
		return false
	}
	expected, err := hex.DecodeString(expectedHex)
	if err != nil || len(expected) != sha256.Size {
		return false
	}
	digest := sha256.Sum256(token)
	return subtle.ConstantTimeCompare(digest[:], expected) == 1
}

type containerInspection struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status   string `json:"Status"`
		Running  bool   `json:"Running"`
		ExitCode int    `json:"ExitCode"`
	} `json:"State"`
}

func (d *RunnerContainers) inspect(ctx context.Context, name string) (containerInspection, bool, error) {
	response, err := d.request(ctx, http.MethodGet, d.apiPath("/containers/")+url.PathEscape(name)+"/json", nil)
	if err != nil {
		return containerInspection{}, false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return containerInspection{}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return containerInspection{}, false, engineError("inspect Docker runner", response)
	}
	var container containerInspection
	if err := decode(response.Body, &container); err != nil {
		return containerInspection{}, false, err
	}
	return container, true, nil
}

func (d *RunnerContainers) start(ctx context.Context, id, name string) error {
	response, err := d.request(ctx, http.MethodPost, d.apiPath("/containers/")+url.PathEscape(id)+"/start", nil)
	if err != nil {
		return uncertainError(name, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotModified {
		return uncertainError(name, engineError("start Docker runner", response))
	}
	return nil
}

func (d *RunnerContainers) request(ctx context.Context, method, path string, value any) (*http.Response, error) {
	var body io.Reader
	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, d.origin+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if value != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Docker Engine request: %w", err)
	}
	return response, nil
}

func (d *RunnerContainers) apiPath(path string) string { return "/" + d.version + path }

func decode(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maximumResponseBytes+1))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func engineError(operation string, response *http.Response) error {
	var value struct {
		Message string `json:"message"`
	}
	_ = decode(response.Body, &value)
	return fmt.Errorf("%s returned HTTP %d", operation, response.StatusCode)
}

func uncertain(name string, cause error) (string, error) { return name, uncertainError(name, cause) }

func uncertainError(_ string, cause error) error {
	return fmt.Errorf("%w: %w", runnercontrol.ErrLaunchUncertain, cause)
}

func containerName(invocationID string) string {
	return "spyglass-runner-" + strings.ReplaceAll(invocationID, "-", "")
}

func matches(container containerInspection, invocation runnercontrol.Invocation, contract, name string) bool {
	return container.ID != "" && container.Name == "/"+name && container.Config.Labels[managedLabel] == "true" && container.Config.Labels[invocationLabel] == invocation.ID && container.Config.Labels[profileLabel] == invocation.Profile && container.Config.Labels[contractLabel] == contract && isHexDigest(container.Config.Labels[tokenDigestLabel])
}

func isHexDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

var _ runnercontrol.Launcher = (*RunnerContainers)(nil)
var _ runnerbroker.IdentityVerifier = (*RunnerContainers)(nil)
