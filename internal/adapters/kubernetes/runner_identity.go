package kubernetes

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const maximumRunnerTokenBytes = 16 << 10

var contractDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

type RunnerIdentityConfig struct {
	Endpoint, Namespace, ReviewerToken, Audience, RunnerServiceAccount string
	HTTPClient                                                         *http.Client
}

// RunnerIdentity verifies a purpose-bound runner token online, then binds its
// Pod UID to the exact controller-created Job and invocation. It never lists
// workload objects and never trusts unverified JWT claims.
type RunnerIdentity struct {
	endpoint, namespace, reviewerToken, audience, runnerServiceAccount string
	http                                                               *http.Client
}

func NewRunnerIdentity(config RunnerIdentityConfig) (*RunnerIdentity, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || parsedEndpoint.Host == "" || parsedEndpoint.User != nil || (parsedEndpoint.Path != "" && parsedEndpoint.Path != "/") || parsedEndpoint.RawQuery != "" || parsedEndpoint.Fragment != "" || (parsedEndpoint.Scheme != "https" && !(parsedEndpoint.Scheme == "http" && config.HTTPClient != nil)) {
		return nil, errors.New("Kubernetes API endpoint is invalid")
	}
	audience, err := url.Parse(strings.TrimSpace(config.Audience))
	if err != nil || audience.Scheme != "https" || audience.Host == "" || audience.User != nil || audience.RawQuery != "" || audience.Fragment != "" || len(audience.String()) > 500 {
		return nil, errors.New("runner broker audience must be an exact HTTPS URL")
	}
	reviewerToken := strings.TrimSpace(config.ReviewerToken)
	if !dnsLabel.MatchString(config.Namespace) || !dnsLabel.MatchString(config.RunnerServiceAccount) || !validPresentedToken(reviewerToken) {
		return nil, errors.New("runner identity namespace, service account, and reviewer token are required")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &RunnerIdentity{endpoint: endpoint, namespace: config.Namespace, reviewerToken: reviewerToken, audience: audience.String(), runnerServiceAccount: config.RunnerServiceAccount, http: client}, nil
}

func NewInClusterRunnerIdentity(config RunnerIdentityConfig) (*RunnerIdentity, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS")
	if host == "" || port == "" {
		return nil, errors.New("Kubernetes in-cluster endpoint is unavailable")
	}
	token, err := os.ReadFile(serviceAccountDirectory + "/token")
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes reviewer token: %w", err)
	}
	ca, err := os.ReadFile(serviceAccountDirectory + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes reviewer CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("Kubernetes reviewer CA is invalid")
	}
	config.Endpoint = "https://" + net.JoinHostPort(host, port)
	config.ReviewerToken = string(token)
	config.HTTPClient = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}}
	return NewRunnerIdentity(config)
}

func (v *RunnerIdentity) Verify(ctx context.Context, token, invocationID string) (runnerbroker.Identity, error) {
	if ids.Validate(invocationID) != nil || !validPresentedToken(token) {
		return runnerbroker.Identity{}, runnerbroker.ErrIdentityDenied
	}
	review, err := v.review(ctx, token)
	if err != nil {
		return runnerbroker.Identity{}, err
	}
	expectedUsername := "system:serviceaccount:" + v.namespace + ":" + v.runnerServiceAccount
	podName, podUID, ok := reviewedPod(review, v.audience, expectedUsername)
	if !ok {
		return runnerbroker.Identity{}, runnerbroker.ErrIdentityDenied
	}
	pod, err := v.pod(ctx, podName)
	if err != nil {
		return runnerbroker.Identity{}, err
	}
	profile, ownerName, ownerUID, ok := verifiedRunnerPod(pod, podName, podUID, invocationID, v.runnerServiceAccount)
	if !ok {
		return runnerbroker.Identity{}, runnerbroker.ErrIdentityDenied
	}
	job, err := v.job(ctx, ownerName)
	if err != nil {
		return runnerbroker.Identity{}, err
	}
	if job.Metadata.Name != jobName(invocationID) || job.Metadata.Name != ownerName || job.Metadata.UID != ownerUID || job.Metadata.DeletionTimestamp != "" || job.Metadata.Labels["spyglass.io/invocation-id"] != invocationID || job.Metadata.Labels["spyglass.io/profile"] != profile || !contractDigest.MatchString(job.Metadata.Annotations["spyglass.io/launch-contract-sha256"]) {
		return runnerbroker.Identity{}, runnerbroker.ErrIdentityDenied
	}
	return runnerbroker.Identity{InvocationID: invocationID, Profile: profile, JobName: ownerName, JobUID: ownerUID, PodName: podName, PodUID: podUID}, nil
}

type tokenReviewStatus struct {
	Authenticated bool            `json:"authenticated"`
	Audiences     []string        `json:"audiences"`
	Error         string          `json:"error"`
	User          tokenReviewUser `json:"user"`
}

type tokenReviewUser struct {
	Username string              `json:"username"`
	UID      string              `json:"uid"`
	Groups   []string            `json:"groups"`
	Extra    map[string][]string `json:"extra"`
}

func (v *RunnerIdentity) review(ctx context.Context, token string) (tokenReviewStatus, error) {
	body, err := json.Marshal(map[string]any{
		"apiVersion": "authentication.k8s.io/v1", "kind": "TokenReview",
		"spec": map[string]any{"token": token, "audiences": []string{v.audience}},
	})
	if err != nil {
		return tokenReviewStatus{}, err
	}
	response, err := v.request(ctx, http.MethodPost, "/apis/authentication.k8s.io/v1/tokenreviews", body)
	if err != nil {
		return tokenReviewStatus{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return tokenReviewStatus{}, fmt.Errorf("Kubernetes TokenReview returned HTTP %d", response.StatusCode)
	}
	var value struct {
		Status tokenReviewStatus `json:"status"`
	}
	if err := decodeBounded(response.Body, &value); err != nil {
		return tokenReviewStatus{}, fmt.Errorf("decode Kubernetes TokenReview: %w", err)
	}
	return value.Status, nil
}

type runnerPod struct {
	Metadata struct {
		Name, UID, DeletionTimestamp string
		Labels                       map[string]string
		OwnerReferences              []struct {
			APIVersion, Kind, Name, UID string
			Controller                  bool
		} `json:"ownerReferences"`
	} `json:"metadata"`
	Spec struct {
		ServiceAccountName string `json:"serviceAccountName"`
	} `json:"spec"`
}

type runnerJob struct {
	Metadata jobMetadata `json:"metadata"`
}

func (v *RunnerIdentity) pod(ctx context.Context, name string) (runnerPod, error) {
	var pod runnerPod
	if err := v.get(ctx, "/api/v1/namespaces/"+url.PathEscape(v.namespace)+"/pods/"+url.PathEscape(name), &pod); err != nil {
		return runnerPod{}, err
	}
	return pod, nil
}

func (v *RunnerIdentity) job(ctx context.Context, name string) (runnerJob, error) {
	var job runnerJob
	if err := v.get(ctx, "/apis/batch/v1/namespaces/"+url.PathEscape(v.namespace)+"/jobs/"+url.PathEscape(name), &job); err != nil {
		return runnerJob{}, err
	}
	return job, nil
}

func (v *RunnerIdentity) get(ctx context.Context, path string, target any) error {
	response, err := v.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusForbidden {
		return runnerbroker.ErrIdentityDenied
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Kubernetes runner identity lookup returned HTTP %d", response.StatusCode)
	}
	if err := decodeBounded(response.Body, target); err != nil {
		return fmt.Errorf("decode Kubernetes runner identity: %w", err)
	}
	return nil
}

func (v *RunnerIdentity) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, v.endpoint+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+v.reviewerToken)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := v.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Kubernetes runner identity request: %w", err)
	}
	return response, nil
}

func reviewedPod(review tokenReviewStatus, audience, username string) (string, string, bool) {
	namespace := strings.Split(username, ":")[2]
	if !review.Authenticated || review.Error != "" || review.User.Username != username || review.User.UID == "" || !containsExact(review.Audiences, audience) || !containsExact(review.User.Groups, "system:serviceaccounts") || !containsExact(review.User.Groups, "system:serviceaccounts:"+namespace) || !containsExact(review.User.Groups, "system:authenticated") {
		return "", "", false
	}
	podNames := review.User.Extra["authentication.kubernetes.io/pod-name"]
	podUIDs := review.User.Extra["authentication.kubernetes.io/pod-uid"]
	if len(podNames) != 1 || len(podUIDs) != 1 || !validDNSSubdomain(podNames[0]) || ids.Validate(podUIDs[0]) != nil {
		return "", "", false
	}
	return podNames[0], podUIDs[0], true
}

func verifiedRunnerPod(pod runnerPod, name, uid, invocationID, serviceAccount string) (string, string, string, bool) {
	profile := pod.Metadata.Labels["spyglass.io/profile"]
	if pod.Metadata.Name != name || pod.Metadata.UID != uid || pod.Metadata.DeletionTimestamp != "" || pod.Spec.ServiceAccountName != serviceAccount || pod.Metadata.Labels["spyglass.io/invocation-id"] != invocationID || !profileName.MatchString(profile) {
		return "", "", "", false
	}
	var ownerName, ownerUID string
	for _, owner := range pod.Metadata.OwnerReferences {
		if !owner.Controller {
			continue
		}
		if owner.APIVersion != "batch/v1" || owner.Kind != "Job" || ownerName != "" || !validDNSSubdomain(owner.Name) || strings.TrimSpace(owner.UID) == "" {
			return "", "", "", false
		}
		ownerName, ownerUID = owner.Name, owner.UID
	}
	return profile, ownerName, ownerUID, ownerName != "" && ownerName == jobName(invocationID)
}

func validPresentedToken(token string) bool {
	if token == "" || len(token) > maximumRunnerTokenBytes || strings.TrimSpace(token) != token {
		return false
	}
	return !strings.ContainsAny(token, " \t\r\n\x00")
}

func validDNSSubdomain(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if !dnsLabel.MatchString(part) {
			return false
		}
	}
	return true
}

func containsExact(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

var _ runnerbroker.IdentityVerifier = (*RunnerIdentity)(nil)
