// Package kubernetes adapts Spyglass runner-control ports to the Kubernetes API.
package kubernetes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
)

const (
	serviceAccountDirectory = "/var/run/secrets/kubernetes.io/serviceaccount"
	runnerIdentityDirectory = "/var/run/secrets/spyglass.io/runner-identity"
	runnerIdentityTokenFile = runnerIdentityDirectory + "/token"
	runnerBrokerCADirectory = "/var/run/secrets/spyglass.io/broker-ca"
	runnerBrokerCAFile      = runnerBrokerCADirectory + "/ca.crt"
	runnerTokenLifetime     = int64(600)
	maximumResponseBytes    = 64 << 10
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var digestImage = regexp.MustCompile(`^\S+@sha256:[0-9a-f]{64}$`)
var profileName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,49}$`)

type ResourceProfile struct {
	CPURequest, CPULimit       string
	MemoryRequest, MemoryLimit string
	EphemeralStorageLimit      string
}

type Config struct {
	Endpoint, Namespace, BearerToken               string
	RunnerImage, RunnerServiceAccount, BrokerURL   string
	RunnerBrokerCAConfigMap                        string
	RunnerRuntimeClass                             string
	HTTPClient                                     *http.Client
	Profiles                                       map[string]ResourceProfile
	ActiveDeadlineSeconds, TTLSecondsAfterFinished int64
}

type RunnerJobs struct {
	endpoint, namespace, token, image, serviceAccount, runtimeClass, brokerURL, brokerCAConfigMap string
	http                                                                                          *http.Client
	profiles                                                                                      map[string]ResourceProfile
	activeDeadline, ttl                                                                           int64
}

func NewRunnerJobs(config Config) (*RunnerJobs, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && config.HTTPClient != nil)) {
		return nil, errors.New("Kubernetes API endpoint is invalid")
	}
	if !dnsLabel.MatchString(config.Namespace) || !dnsLabel.MatchString(config.RunnerServiceAccount) || !dnsLabel.MatchString(config.RunnerRuntimeClass) || !dnsLabel.MatchString(config.RunnerBrokerCAConfigMap) || strings.TrimSpace(config.BearerToken) == "" {
		return nil, errors.New("Kubernetes namespace, runner service account, runtime class, and bearer token are required")
	}
	if len(config.RunnerImage) > 500 || !digestImage.MatchString(config.RunnerImage) {
		return nil, errors.New("runner image must be pinned by a lowercase SHA-256 digest")
	}
	broker, err := url.Parse(config.BrokerURL)
	if err != nil || broker.Scheme != "https" || broker.Host == "" || broker.User != nil || broker.RawQuery != "" || broker.Fragment != "" {
		return nil, errors.New("runner broker URL must be an HTTPS origin/path without credentials, query, or fragment")
	}
	if config.ActiveDeadlineSeconds < 30 || config.ActiveDeadlineSeconds > 24*60*60 || config.TTLSecondsAfterFinished < 60 || config.TTLSecondsAfterFinished > 7*24*60*60 {
		return nil, errors.New("runner Job deadline or retention is out of bounds")
	}
	if len(config.Profiles) == 0 || len(config.Profiles) > 20 {
		return nil, errors.New("at least one bounded runner resource profile is required")
	}
	profiles := make(map[string]ResourceProfile, len(config.Profiles))
	for name, profile := range config.Profiles {
		if !validProfile(name, profile) {
			return nil, fmt.Errorf("runner resource profile %q is invalid", name)
		}
		profiles[name] = profile
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &RunnerJobs{endpoint: endpoint, namespace: config.Namespace, token: strings.TrimSpace(config.BearerToken), image: config.RunnerImage, serviceAccount: config.RunnerServiceAccount, runtimeClass: config.RunnerRuntimeClass, brokerURL: config.BrokerURL, brokerCAConfigMap: config.RunnerBrokerCAConfigMap, http: client, profiles: profiles, activeDeadline: config.ActiveDeadlineSeconds, ttl: config.TTLSecondsAfterFinished}, nil
}

// NewInClusterRunnerJobs uses the projected service-account token and CA. The
// controller pod is the only workload that receives this Kubernetes identity.
func NewInClusterRunnerJobs(config Config) (*RunnerJobs, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS")
	if host == "" || port == "" {
		return nil, errors.New("Kubernetes in-cluster endpoint is unavailable")
	}
	token, err := os.ReadFile(serviceAccountDirectory + "/token")
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes service-account token: %w", err)
	}
	ca, err := os.ReadFile(serviceAccountDirectory + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes service-account CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("Kubernetes service-account CA is invalid")
	}
	config.Endpoint = "https://" + net.JoinHostPort(host, port)
	config.BearerToken = string(token)
	config.HTTPClient = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}}
	return NewRunnerJobs(config)
}

func (k *RunnerJobs) Ensure(ctx context.Context, invocation runnercontrol.Invocation) (string, error) {
	profile, exists := k.profiles[invocation.Profile]
	if !exists {
		return "", fmt.Errorf("runner profile %q is not deployed", invocation.Profile)
	}
	name := jobName(invocation.ID)
	job, contract, err := k.job(invocation, name, profile)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(job)
	if err != nil {
		return "", fmt.Errorf("encode runner Job: %w", err)
	}
	response, err := k.request(ctx, http.MethodPost, k.jobsPath(), body)
	if err != nil {
		return uncertainLaunch(name, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusCreated {
		metadata, err := decodeJobMetadata(response.Body)
		if err != nil || metadata.Name != name || metadata.Labels["spyglass.io/invocation-id"] != invocation.ID || metadata.Annotations["spyglass.io/launch-contract-sha256"] != contract {
			return uncertainLaunch(name, errors.New("Kubernetes created a runner Job with mismatched identity"))
		}
		return name, nil
	}
	if response.StatusCode != http.StatusConflict {
		responseErr := responseError("create runner Job", response)
		if response.StatusCode >= http.StatusInternalServerError {
			return uncertainLaunch(name, responseErr)
		}
		return "", responseErr
	}
	response.Body.Close()
	response, err = k.request(ctx, http.MethodGet, k.jobPath(name), nil)
	if err != nil {
		return uncertainLaunch(name, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return uncertainLaunch(name, responseError("inspect conflicting runner Job", response))
	}
	metadata, err := decodeJobMetadata(response.Body)
	if err != nil || metadata.Name != name || metadata.Labels["spyglass.io/invocation-id"] != invocation.ID || metadata.Labels["spyglass.io/profile"] != invocation.Profile || metadata.Annotations["spyglass.io/launch-contract-sha256"] != contract {
		return uncertainLaunch(name, errors.New("existing Kubernetes runner Job conflicts with the invocation contract"))
	}
	return name, nil
}

// Cancel deletes only the exact Job contract owned by the invocation. UID and
// resource-version preconditions close the gap between verification and delete,
// while foreground propagation makes a later 404 evidence that known blocking
// dependent Pod API objects were removed before Account capacity is released.
func (k *RunnerJobs) Cancel(ctx context.Context, invocation runnercontrol.Invocation) error {
	if invocation.State != "canceling" || invocation.JobName == "" || invocation.JobName != jobName(invocation.ID) {
		return errors.New("canceling runner Job identity is invalid")
	}
	profile, exists := k.profiles[invocation.Profile]
	if !exists {
		return fmt.Errorf("runner profile %q is not deployed", invocation.Profile)
	}
	_, contract, err := k.job(invocation, invocation.JobName, profile)
	if err != nil {
		return err
	}
	response, err := k.request(ctx, http.MethodGet, k.jobPath(invocation.JobName), nil)
	if err != nil {
		return err
	}
	if response.StatusCode == http.StatusNotFound {
		response.Body.Close()
		return nil
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return responseError("verify canceling runner Job", response)
	}
	metadata, decodeErr := decodeJobMetadata(response.Body)
	response.Body.Close()
	if decodeErr != nil || !matchesJobContract(metadata, invocation, contract) || metadata.UID == "" || metadata.ResourceVersion == "" {
		return errors.New("canceling Kubernetes runner Job does not match the launch contract")
	}
	deleteOptions := map[string]any{
		"apiVersion": "v1", "kind": "DeleteOptions", "gracePeriodSeconds": int64(0), "propagationPolicy": "Foreground",
		"preconditions": map[string]string{"uid": metadata.UID, "resourceVersion": metadata.ResourceVersion},
	}
	body, err := json.Marshal(deleteOptions)
	if err != nil {
		return err
	}
	response, err = k.request(ctx, http.MethodDelete, k.jobPath(invocation.JobName), body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusAccepted || response.StatusCode == http.StatusNotFound {
		return nil
	}
	return responseError("delete canceling runner Job", response)
}

func (k *RunnerJobs) Inspect(ctx context.Context, invocation runnercontrol.Invocation) (runnercontrol.TerminalStatus, error) {
	if (invocation.State != "launch_uncertain" && invocation.State != "launched" && invocation.State != "canceling") || invocation.JobName == "" || invocation.JobName != jobName(invocation.ID) {
		return runnercontrol.TerminalStatus{}, errors.New("runner Job identity is invalid")
	}
	profile, exists := k.profiles[invocation.Profile]
	if !exists {
		return runnercontrol.TerminalStatus{}, fmt.Errorf("runner profile %q is not deployed", invocation.Profile)
	}
	_, contract, err := k.job(invocation, invocation.JobName, profile)
	if err != nil {
		return runnercontrol.TerminalStatus{}, err
	}
	response, err := k.request(ctx, http.MethodGet, k.jobPath(invocation.JobName), nil)
	if err != nil {
		return runnercontrol.TerminalStatus{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		if invocation.State == "canceling" {
			return runnercontrol.TerminalStatus{Terminal: true, Outcome: "canceled"}, nil
		}
		if invocation.State == "launch_uncertain" {
			return runnercontrol.TerminalStatus{}, nil
		}
		return runnercontrol.TerminalStatus{}, errors.New("launched Kubernetes runner Job was not found")
	}
	if response.StatusCode != http.StatusOK {
		return runnercontrol.TerminalStatus{}, responseError("inspect runner Job", response)
	}
	var job struct {
		Metadata jobMetadata `json:"metadata"`
		Status   struct {
			Conditions []struct {
				Type, Status string
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := decodeBounded(response.Body, &job); err != nil {
		return runnercontrol.TerminalStatus{}, fmt.Errorf("decode runner Job status: %w", err)
	}
	if !matchesJobContract(job.Metadata, invocation, contract) {
		return runnercontrol.TerminalStatus{}, errors.New("runner Job status identity does not match the launch contract")
	}
	if invocation.State == "canceling" {
		return runnercontrol.TerminalStatus{Observed: true}, nil
	}
	for _, condition := range job.Status.Conditions {
		if condition.Status != "True" {
			continue
		}
		switch condition.Type {
		case "Complete":
			return runnercontrol.TerminalStatus{Observed: true, Terminal: true, Outcome: "completed"}, nil
		case "Failed":
			return runnercontrol.TerminalStatus{Observed: true, Terminal: true, Outcome: "execution_failed"}, nil
		}
	}
	return runnercontrol.TerminalStatus{Observed: true}, nil
}

func uncertainLaunch(name string, cause error) (string, error) {
	return name, fmt.Errorf("%w: %w", runnercontrol.ErrLaunchUncertain, cause)
}

func (k *RunnerJobs) job(invocation runnercontrol.Invocation, name string, profile ResourceProfile) (map[string]any, string, error) {
	contractInput := struct {
		Invocation, Profile, Image, ServiceAccount, RuntimeClass, Broker, BrokerCAConfigMap string
		Resources                                                                           ResourceProfile
		Deadline, TTL, TokenLifetime                                                        int64
	}{invocation.ID, invocation.Profile, k.image, k.serviceAccount, k.runtimeClass, k.brokerURL, k.brokerCAConfigMap, profile, k.activeDeadline, k.ttl, runnerTokenLifetime}
	rawContract, err := json.Marshal(contractInput)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(rawContract)
	contract := hex.EncodeToString(digest[:])
	labels := map[string]string{"app.kubernetes.io/name": "spyglass-runner", "app.kubernetes.io/part-of": "spyglass", "app.kubernetes.io/managed-by": "spyglass-runner-controller", "spyglass.io/invocation-id": invocation.ID, "spyglass.io/profile": invocation.Profile}
	containerSecurity := map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "runAsNonRoot": true, "runAsUser": int64(65532), "runAsGroup": int64(65532), "capabilities": map[string]any{"drop": []string{"ALL"}}, "seccompProfile": map[string]any{"type": "RuntimeDefault"}}
	job := map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{"name": name, "namespace": k.namespace, "labels": labels, "annotations": map[string]string{"spyglass.io/launch-contract-sha256": contract}},
		"spec": map[string]any{
			"backoffLimit": int32(0), "activeDeadlineSeconds": k.activeDeadline, "ttlSecondsAfterFinished": k.ttl,
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"serviceAccountName": k.serviceAccount, "automountServiceAccountToken": false, "runtimeClassName": k.runtimeClass, "restartPolicy": "Never", "enableServiceLinks": false,
					"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": int64(65532), "runAsGroup": int64(65532), "fsGroup": int64(65532), "seccompProfile": map[string]any{"type": "RuntimeDefault"}},
					"containers": []any{map[string]any{
						"name": "runner", "image": k.image, "imagePullPolicy": "IfNotPresent", "workingDir": "/work",
						"args":            []string{"runner-invocation", "--broker-url=" + k.brokerURL, "--invocation-id=" + invocation.ID, "--identity-token-file=" + runnerIdentityTokenFile, "--broker-ca-file=" + runnerBrokerCAFile},
						"resources":       map[string]any{"requests": map[string]string{"cpu": profile.CPURequest, "memory": profile.MemoryRequest}, "limits": map[string]string{"cpu": profile.CPULimit, "memory": profile.MemoryLimit, "ephemeral-storage": profile.EphemeralStorageLimit}},
						"securityContext": containerSecurity,
						"volumeMounts":    []any{map[string]any{"name": "work", "mountPath": "/work"}, map[string]any{"name": "tmp", "mountPath": "/tmp"}, map[string]any{"name": "broker-identity", "mountPath": runnerIdentityDirectory, "readOnly": true}, map[string]any{"name": "broker-ca", "mountPath": runnerBrokerCADirectory, "readOnly": true}},
					}},
					"volumes": []any{
						map[string]any{"name": "work", "emptyDir": map[string]any{"sizeLimit": profile.EphemeralStorageLimit}},
						map[string]any{"name": "tmp", "emptyDir": map[string]any{"medium": "Memory", "sizeLimit": "64Mi"}},
						map[string]any{"name": "broker-identity", "projected": map[string]any{"defaultMode": int32(0o400), "sources": []any{map[string]any{"serviceAccountToken": map[string]any{"audience": k.brokerURL, "expirationSeconds": runnerTokenLifetime, "path": "token"}}}}},
						map[string]any{"name": "broker-ca", "configMap": map[string]any{"name": k.brokerCAConfigMap, "defaultMode": int32(0o444), "items": []any{map[string]any{"key": "ca.crt", "path": "ca.crt"}}}},
					},
				},
			},
		},
	}
	return job, contract, nil
}

type jobMetadata struct {
	Name              string            `json:"name"`
	UID               string            `json:"uid"`
	ResourceVersion   string            `json:"resourceVersion"`
	DeletionTimestamp string            `json:"deletionTimestamp"`
	Labels            map[string]string `json:"labels"`
	Annotations       map[string]string `json:"annotations"`
}

func matchesJobContract(metadata jobMetadata, invocation runnercontrol.Invocation, contract string) bool {
	return metadata.Name == invocation.JobName &&
		metadata.Labels["spyglass.io/invocation-id"] == invocation.ID &&
		metadata.Labels["spyglass.io/profile"] == invocation.Profile &&
		metadata.Annotations["spyglass.io/launch-contract-sha256"] == contract
}

func decodeJobMetadata(reader io.Reader) (jobMetadata, error) {
	var value struct {
		Metadata jobMetadata `json:"metadata"`
	}
	if err := decodeBounded(reader, &value); err != nil {
		return jobMetadata{}, err
	}
	return value.Metadata, nil
}

func decodeBounded(reader io.Reader, target any) error {
	limited := io.LimitReader(reader, maximumResponseBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(raw) > maximumResponseBytes {
		return errors.New("Kubernetes API response exceeded the limit")
	}
	return json.Unmarshal(raw, target)
}

func (k *RunnerJobs) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, k.endpoint+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+k.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := k.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Kubernetes API request: %w", err)
	}
	return response, nil
}

func responseError(action string, response *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	return fmt.Errorf("%s returned HTTP %d: %s", action, response.StatusCode, strings.TrimSpace(string(raw)))
}

func (k *RunnerJobs) jobsPath() string {
	return "/apis/batch/v1/namespaces/" + url.PathEscape(k.namespace) + "/jobs"
}

func (k *RunnerJobs) jobPath(name string) string { return k.jobsPath() + "/" + url.PathEscape(name) }

func jobName(invocationID string) string {
	return "spyglass-runner-" + strings.ReplaceAll(strings.ToLower(invocationID), "-", "")
}

func validProfile(name string, profile ResourceProfile) bool {
	if !profileName.MatchString(name) {
		return false
	}
	for _, value := range []string{profile.CPURequest, profile.CPULimit, profile.MemoryRequest, profile.MemoryLimit, profile.EphemeralStorageLimit} {
		if value == "" || len(value) > 20 || strings.ContainsAny(value, " \t\r\n\x00") {
			return false
		}
	}
	return true
}

var _ runnercontrol.Launcher = (*RunnerJobs)(nil)
