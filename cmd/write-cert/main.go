// write-cert produces content-free idempotency and latency evidence for a
// bounded synthetic Work write profile against one deployed Spyglass release.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const maxResponseBody = 1 << 20

var (
	machineName  = regexp.MustCompile(`^[a-z][a-z0-9-]{1,47}$`)
	cookieEnvRE  = regexp.MustCompile(`^SPYGLASS_WRITE_COOKIE_[A-Z0-9_]{1,48}$`)
	accountEnvRE = regexp.MustCompile(`^SPYGLASS_WRITE_ACCOUNT_ID_[A-Z0-9_]{1,48}$`)
	revisionRE   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestRE     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	etagRE       = regexp.MustCompile(`^W/"[1-9][0-9]*"$`)
)

type plan struct {
	SchemaVersion         int     `json:"schema_version"`
	Environment           string  `json:"environment"`
	Revision              string  `json:"revision"`
	ImageDigest           string  `json:"image_digest"`
	Origin                string  `json:"origin"`
	RunID                 string  `json:"run_id"`
	Operations            int     `json:"operations"`
	OperationsPerSecond   int     `json:"operations_per_second"`
	Concurrency           int     `json:"concurrency"`
	RequestTimeoutSeconds int     `json:"request_timeout_seconds"`
	MaxCreateP95MS        int64   `json:"max_create_p95_ms"`
	MaxReplayP95MS        int64   `json:"max_replay_p95_ms"`
	MaxErrorRate          float64 `json:"max_error_rate"`
	Actors                []actor `json:"actors"`
}

type actor struct {
	Name         string `json:"name"`
	Weight       int    `json:"weight"`
	CookieEnv    string `json:"cookie_env"`
	AccountIDEnv string `json:"account_id_env"`
}

type credential struct {
	actor     actor
	cookie    string
	accountID string
}

type workCase struct {
	actor   credential
	ordinal int
}

type attempt struct {
	status, durationMS int64
	etag, location     string
	bodyDigest         [32]byte
	errorCode          string
	requested          bool
}

type observation struct {
	actor                      string
	createMS, replayMS         int64
	createStatus, replayStatus int
	requests                   int
	errorCode                  string
}

type report struct {
	SchemaVersion         int           `json:"schema_version"`
	Environment           string        `json:"environment"`
	Revision              string        `json:"revision"`
	ImageDigest           string        `json:"image_digest"`
	PlanSHA256            string        `json:"plan_sha256"`
	StartedAt             time.Time     `json:"started_at"`
	CompletedAt           time.Time     `json:"completed_at"`
	Operations            int           `json:"operations"`
	OperationsPerSecond   int           `json:"operations_per_second"`
	Concurrency           int           `json:"concurrency"`
	RequestTimeoutSeconds int           `json:"request_timeout_seconds"`
	PlannedRequests       int           `json:"planned_requests"`
	ScheduledOperations   int           `json:"scheduled_operations"`
	CompletedOperations   int           `json:"completed_operations"`
	CompletedRequests     int           `json:"completed_requests"`
	MaxCreateP95MS        int64         `json:"max_create_p95_ms"`
	MaxReplayP95MS        int64         `json:"max_replay_p95_ms"`
	MaxErrorRate          float64       `json:"max_error_rate"`
	Success               bool          `json:"success"`
	Results               []actorResult `json:"results"`
}

type actorResult struct {
	Actor             string         `json:"actor"`
	Operations        int            `json:"operations"`
	Requests          int            `json:"requests"`
	Errors            int            `json:"errors"`
	ErrorRate         float64        `json:"error_rate"`
	CreateP50MS       int64          `json:"create_p50_ms"`
	CreateP95MS       int64          `json:"create_p95_ms"`
	CreateP99MS       int64          `json:"create_p99_ms"`
	CreateMaxMS       int64          `json:"create_max_ms"`
	ReplayP50MS       int64          `json:"replay_p50_ms"`
	ReplayP95MS       int64          `json:"replay_p95_ms"`
	ReplayP99MS       int64          `json:"replay_p99_ms"`
	ReplayMaxMS       int64          `json:"replay_max_ms"`
	CreateStatusCodes map[string]int `json:"create_status_codes"`
	ReplayStatusCodes map[string]int `json:"replay_status_codes"`
	ErrorCodes        map[string]int `json:"error_codes"`
	Passed            bool           `json:"passed"`
}

type actorSamples struct {
	create, replay             []int64
	requests, errors           int
	createStatus, replayStatus map[string]int
	codes                      map[string]int
}

func main() {
	planPath := flag.String("plan", "", "reviewed synthetic write plan JSON")
	output := flag.String("output", "-", "new evidence JSON file, or - for stdout")
	flag.Parse()
	if flag.NArg() != 0 || *planPath == "" {
		fmt.Fprintln(os.Stderr, "usage: write-cert -plan <plan.json> -output <new-evidence.json>")
		os.Exit(2)
	}
	value, raw, err := readPlan(*planPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plan:", err)
		os.Exit(2)
	}
	credentials, err := loadCredentials(value.Actors, os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "credentials:", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	evidence := execute(ctx, value, raw, credentials, httpClient(value.Concurrency, value.RequestTimeoutSeconds), time.Now)
	if err := writeReport(*output, evidence); err != nil {
		fmt.Fprintln(os.Stderr, "write evidence:", err)
		os.Exit(2)
	}
	if !evidence.Success {
		os.Exit(1)
	}
}

func readPlan(path string) (plan, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return plan{}, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value plan
	if err := decoder.Decode(&value); err != nil {
		return plan{}, nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return plan{}, nil, errors.New("plan must contain exactly one JSON value")
	}
	if err := value.validate(); err != nil {
		return plan{}, nil, err
	}
	return value, raw, nil
}

func (p plan) validate() error {
	if p.SchemaVersion != 1 || !machineName.MatchString(p.Environment) || !revisionRE.MatchString(p.Revision) || !digestRE.MatchString(p.ImageDigest) || ids.Validate(p.RunID) != nil {
		return errors.New("schema, environment, release identity, or run identifier is invalid")
	}
	origin, err := url.Parse(p.Origin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.String() != p.Origin {
		return errors.New("origin must be an exact HTTPS origin")
	}
	if p.Operations < 1 || p.Operations > 500 || p.OperationsPerSecond < 1 || p.OperationsPerSecond > 50 || p.Concurrency < 1 || p.Concurrency > 50 || p.RequestTimeoutSeconds < 1 || p.RequestTimeoutSeconds > 60 {
		return errors.New("operation, rate, concurrency, or timeout bound is invalid")
	}
	if p.MaxCreateP95MS < 1 || p.MaxCreateP95MS > 60_000 || p.MaxReplayP95MS < 1 || p.MaxReplayP95MS > 60_000 || p.MaxErrorRate < 0 || p.MaxErrorRate > 0.25 || len(p.Actors) < 1 || len(p.Actors) > 100 {
		return errors.New("threshold or actor cardinality is invalid")
	}
	seenNames, seenCookies, seenAccounts := map[string]bool{}, map[string]bool{}, map[string]bool{}
	weight := 0
	for _, item := range p.Actors {
		if !machineName.MatchString(item.Name) || seenNames[item.Name] || item.Weight < 1 || item.Weight > 100 || !cookieEnvRE.MatchString(item.CookieEnv) || !accountEnvRE.MatchString(item.AccountIDEnv) || seenCookies[item.CookieEnv] || seenAccounts[item.AccountIDEnv] {
			return errors.New("actor name, weight, or environment reference is invalid")
		}
		seenNames[item.Name], seenCookies[item.CookieEnv], seenAccounts[item.AccountIDEnv] = true, true, true
		weight += item.Weight
	}
	if weight > 1000 || p.Operations < weight {
		return errors.New("operations must cover one complete weighted actor cycle")
	}
	return nil
}

func loadCredentials(actors []actor, lookup func(string) (string, bool)) ([]credential, error) {
	result := make([]credential, 0, len(actors))
	seenAccounts := map[string]bool{}
	for _, item := range actors {
		cookie, cookieOK := lookup(item.CookieEnv)
		accountID, accountOK := lookup(item.AccountIDEnv)
		if !cookieOK || cookie == "" || len(cookie) > 8192 || strings.ContainsAny(cookie, "\r\n\x00") {
			return nil, fmt.Errorf("%s is missing or invalid", item.CookieEnv)
		}
		if !accountOK || ids.Validate(accountID) != nil || seenAccounts[accountID] {
			return nil, fmt.Errorf("%s is missing, invalid, or duplicated", item.AccountIDEnv)
		}
		seenAccounts[accountID] = true
		result = append(result, credential{actor: item, cookie: cookie, accountID: accountID})
	}
	return result, nil
}

func httpClient(concurrency, timeoutSeconds int) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = concurrency
	transport.MaxIdleConnsPerHost = concurrency
	transport.MaxConnsPerHost = concurrency
	transport.ForceAttemptHTTP2 = true
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: transport, Timeout: time.Duration(timeoutSeconds) * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("redirect") }}
}

func execute(ctx context.Context, p plan, raw []byte, credentials []credential, client *http.Client, now func() time.Time) report {
	planDigest := sha256.Sum256(raw)
	value := report{SchemaVersion: 1, Environment: p.Environment, Revision: p.Revision, ImageDigest: p.ImageDigest, PlanSHA256: hex.EncodeToString(planDigest[:]),
		Operations: p.Operations, OperationsPerSecond: p.OperationsPerSecond, Concurrency: p.Concurrency, RequestTimeoutSeconds: p.RequestTimeoutSeconds,
		PlannedRequests: p.Operations * 2, MaxCreateP95MS: p.MaxCreateP95MS, MaxReplayP95MS: p.MaxReplayP95MS, MaxErrorRate: p.MaxErrorRate}
	cases := expandCases(credentials)
	maximumSeconds := (p.Operations+p.OperationsPerSecond-1)/p.OperationsPerSecond + 2*p.RequestTimeoutSeconds + 5
	window, cancel := context.WithTimeout(ctx, time.Duration(maximumSeconds)*time.Second)
	defer cancel()
	jobs := make(chan workCase, p.Concurrency)
	observations := make(chan observation, p.Operations)
	var workers sync.WaitGroup
	for range p.Concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				observations <- exercise(window, p, item, client)
			}
		}()
	}
	value.StartedAt = now().UTC()
	interval := time.Second / time.Duration(p.OperationsPerSecond)
	ticker := time.NewTicker(interval)
	for ordinal := 1; ordinal <= p.Operations; ordinal++ {
		if ordinal > 1 {
			select {
			case <-window.Done():
				ordinal = p.Operations + 1
				continue
			case <-ticker.C:
			}
		}
		select {
		case jobs <- workCase{actor: cases[(ordinal-1)%len(cases)], ordinal: ordinal}:
			value.ScheduledOperations++
		case <-window.Done():
			ordinal = p.Operations + 1
		}
	}
	ticker.Stop()
	close(jobs)
	workers.Wait()
	close(observations)
	groups := map[string]*actorSamples{}
	for item := range observations {
		group := groups[item.actor]
		if group == nil {
			group = &actorSamples{createStatus: map[string]int{}, replayStatus: map[string]int{}, codes: map[string]int{}}
			groups[item.actor] = group
		}
		group.create, group.replay = append(group.create, item.createMS), append(group.replay, item.replayMS)
		if item.createStatus != 0 {
			group.createStatus[fmt.Sprint(item.createStatus)]++
		}
		if item.replayStatus != 0 {
			group.replayStatus[fmt.Sprint(item.replayStatus)]++
		}
		if item.errorCode != "" {
			group.errors++
			group.codes[item.errorCode]++
		}
		group.requests += item.requests
		value.CompletedOperations++
		value.CompletedRequests += item.requests
	}
	value.CompletedAt = now().UTC()
	value.Results = summarize(p, groups)
	value.Success = value.ScheduledOperations == p.Operations && value.CompletedOperations == p.Operations && value.CompletedRequests == value.PlannedRequests && len(value.Results) == len(p.Actors)
	for _, item := range value.Results {
		value.Success = value.Success && item.Passed
	}
	return value
}

func expandCases(credentials []credential) []credential {
	var result []credential
	for _, item := range credentials {
		for range item.actor.Weight {
			result = append(result, item)
		}
	}
	return result
}

func exercise(ctx context.Context, p plan, item workCase, client *http.Client) observation {
	result := observation{actor: item.actor.actor.Name}
	operationID, err := ids.Derive(p.RunID, fmt.Sprintf("work/%s/%06d", item.actor.actor.Name, item.ordinal))
	if err != nil {
		result.errorCode = "identity"
		return result
	}
	body, err := json.Marshal(map[string]any{"kind": "todo", "title": fmt.Sprintf("Certification %s %06d", p.RunID[:8], item.ordinal), "description": "Generated synthetic Work write-cert item.", "priority": "normal", "assignment": map[string]string{"responsibility": "shared"}, "reason": "synthetic write certification"})
	if err != nil {
		result.errorCode = "payload"
		return result
	}
	path := fmt.Sprintf("/api/v1/accounts/%s/work-items", url.PathEscape(item.actor.accountID))
	create := postWork(ctx, p.Origin+path, item.actor, operationID, body, client)
	replay := postWork(ctx, p.Origin+path, item.actor, operationID, body, client)
	if create.requested {
		result.requests++
	}
	if replay.requested {
		result.requests++
	}
	result.createMS, result.replayMS = create.durationMS, replay.durationMS
	result.createStatus, result.replayStatus = int(create.status), int(replay.status)
	switch {
	case create.errorCode != "":
		result.errorCode = "create_" + create.errorCode
	case replay.errorCode != "":
		result.errorCode = "replay_" + replay.errorCode
	case create.status != replay.status || create.etag != replay.etag || create.location != replay.location || create.bodyDigest != replay.bodyDigest:
		result.errorCode = "idempotency_mismatch"
	}
	return result
}

func postWork(ctx context.Context, target string, actor credential, operationID string, body []byte, client *http.Client) attempt {
	started := time.Now()
	result := attempt{}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err == nil {
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", operationID)
		request.Header.Set("Cookie", actor.cookie)
		var response *http.Response
		result.requested = true
		response, err = client.Do(request)
		if err == nil {
			result.status, result.etag, result.location = int64(response.StatusCode), response.Header.Get("ETag"), response.Header.Get("Location")
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
			closeErr := response.Body.Close()
			result.bodyDigest = sha256.Sum256(raw)
			var view struct {
				ID      string `json:"id"`
				Version uint64 `json:"version"`
			}
			switch {
			case readErr != nil || closeErr != nil:
				result.errorCode = "read"
			case len(raw) > maxResponseBody:
				result.errorCode = "body_limit"
			case response.StatusCode != http.StatusCreated:
				result.errorCode = "status"
			case !matchesMediaType(response.Header.Get("Content-Type"), "application/json"):
				result.errorCode = "content_type"
			case json.Unmarshal(raw, &view) != nil || view.ID != operationID || ids.Validate(view.ID) != nil || view.Version == 0 || !etagRE.MatchString(result.etag) || result.location != fmt.Sprintf("/api/v1/accounts/%s/work-items/%s", actor.accountID, view.ID):
				result.errorCode = "shape"
			}
		}
	}
	if err != nil {
		result.errorCode = "transport"
	}
	result.durationMS = time.Since(started).Milliseconds()
	return result
}

func matchesMediaType(actual, expected string) bool {
	mediaType, _, err := mime.ParseMediaType(actual)
	return err == nil && strings.EqualFold(mediaType, expected)
}

func summarize(p plan, groups map[string]*actorSamples) []actorResult {
	result := make([]actorResult, 0, len(groups))
	for _, subject := range p.Actors {
		group := groups[subject.Name]
		if group == nil || len(group.create) == 0 {
			continue
		}
		sort.Slice(group.create, func(i, j int) bool { return group.create[i] < group.create[j] })
		sort.Slice(group.replay, func(i, j int) bool { return group.replay[i] < group.replay[j] })
		count := len(group.create)
		item := actorResult{Actor: subject.Name, Operations: count, Requests: group.requests, Errors: group.errors, ErrorRate: float64(group.errors) / float64(count),
			CreateP50MS: percentile(group.create, 50), CreateP95MS: percentile(group.create, 95), CreateP99MS: percentile(group.create, 99), CreateMaxMS: group.create[count-1],
			ReplayP50MS: percentile(group.replay, 50), ReplayP95MS: percentile(group.replay, 95), ReplayP99MS: percentile(group.replay, 99), ReplayMaxMS: group.replay[count-1],
			CreateStatusCodes: group.createStatus, ReplayStatusCodes: group.replayStatus, ErrorCodes: group.codes}
		item.Passed = item.CreateP95MS <= p.MaxCreateP95MS && item.ReplayP95MS <= p.MaxReplayP95MS && item.ErrorRate <= p.MaxErrorRate
		result = append(result, item)
	}
	slices.SortFunc(result, func(a, b actorResult) int { return strings.Compare(a.Actor, b.Actor) })
	return result
}

func percentile(values []int64, percent int) int64 {
	index := (len(values)*percent + 99) / 100
	if index < 1 {
		index = 1
	}
	return values[index-1]
}

func writeReport(path string, value report) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if path == "-" {
		_, err = os.Stdout.Write(raw)
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.Write(raw); err != nil {
		return err
	}
	return file.Sync()
}
