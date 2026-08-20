// load-cert produces content-free latency, error, and fairness evidence for a
// reviewed synthetic read workload against one deployed Spyglass release.
package main

import (
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
	cookieEnvRE  = regexp.MustCompile(`^SPYGLASS_LOAD_COOKIE_[A-Z0-9_]{1,48}$`)
	accountEnvRE = regexp.MustCompile(`^SPYGLASS_LOAD_ACCOUNT_ID_[A-Z0-9_]{1,48}$`)
	revisionRE   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestRE     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type plan struct {
	SchemaVersion     int     `json:"schema_version"`
	Environment       string  `json:"environment"`
	Revision          string  `json:"revision"`
	ImageDigest       string  `json:"image_digest"`
	Origin            string  `json:"origin"`
	WarmupSeconds     int     `json:"warmup_seconds"`
	DurationSeconds   int     `json:"duration_seconds"`
	RequestsPerSecond int     `json:"requests_per_second"`
	Concurrency       int     `json:"concurrency"`
	MaxP95MS          int64   `json:"max_p95_ms"`
	MaxErrorRate      float64 `json:"max_error_rate"`
	Actors            []actor `json:"actors"`
	Routes            []route `json:"routes"`
}

type actor struct {
	Name         string `json:"name"`
	Weight       int    `json:"weight"`
	CookieEnv    string `json:"cookie_env,omitempty"`
	AccountIDEnv string `json:"account_id_env,omitempty"`
}

type route struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Weight      int    `json:"weight"`
	Status      int    `json:"expected_status"`
	ContentType string `json:"expected_content_type"`
}

type report struct {
	SchemaVersion     int       `json:"schema_version"`
	Environment       string    `json:"environment"`
	Revision          string    `json:"revision"`
	ImageDigest       string    `json:"image_digest"`
	PlanSHA256        string    `json:"plan_sha256"`
	StartedAt         time.Time `json:"started_at"`
	CompletedAt       time.Time `json:"completed_at"`
	WarmupSeconds     int       `json:"warmup_seconds"`
	DurationSeconds   int       `json:"duration_seconds"`
	RequestsPerSecond int       `json:"requests_per_second"`
	Concurrency       int       `json:"concurrency"`
	PlannedRequests   int       `json:"planned_requests"`
	ScheduledRequests int       `json:"scheduled_requests"`
	CompletedRequests int       `json:"completed_requests"`
	MaxP95MS          int64     `json:"max_p95_ms"`
	MaxErrorRate      float64   `json:"max_error_rate"`
	Success           bool      `json:"success"`
	Results           []result  `json:"results"`
}

type result struct {
	Actor       string         `json:"actor"`
	Route       string         `json:"route"`
	Samples     int            `json:"samples"`
	Errors      int            `json:"errors"`
	ErrorRate   float64        `json:"error_rate"`
	P50MS       int64          `json:"p50_ms"`
	P95MS       int64          `json:"p95_ms"`
	P99MS       int64          `json:"p99_ms"`
	MaxMS       int64          `json:"max_ms"`
	StatusCodes map[string]int `json:"status_codes"`
	ErrorCodes  map[string]int `json:"error_codes"`
	Passed      bool           `json:"passed"`
}

type credential struct {
	actor     actor
	cookie    string
	accountID string
}

type requestCase struct {
	actor credential
	route route
}

type observation struct {
	actor, route, errorCode string
	status                  int
	durationMS              int64
}

type samples struct {
	durations []int64
	errors    int
	statuses  map[string]int
	codes     map[string]int
}

func main() {
	planPath := flag.String("plan", "", "reviewed load plan JSON")
	output := flag.String("output", "-", "new evidence JSON file, or - for stdout")
	flag.Parse()
	if flag.NArg() != 0 || *planPath == "" {
		fmt.Fprintln(os.Stderr, "usage: load-cert -plan <plan.json> -output <new-evidence.json>")
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
	evidence := execute(ctx, value, raw, credentials, httpClient(value.Concurrency), time.Now)
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
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
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
	if p.SchemaVersion != 1 || !machineName.MatchString(p.Environment) || !revisionRE.MatchString(p.Revision) || !digestRE.MatchString(p.ImageDigest) {
		return errors.New("schema, environment, revision, or image digest is invalid")
	}
	origin, err := url.Parse(p.Origin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.String() != p.Origin {
		return errors.New("origin must be an exact HTTPS origin")
	}
	if p.WarmupSeconds < 0 || p.WarmupSeconds > 60 || p.DurationSeconds < 10 || p.DurationSeconds > 900 || p.RequestsPerSecond < 1 || p.RequestsPerSecond > 1000 || p.Concurrency < 1 || p.Concurrency > 500 {
		return errors.New("warmup, duration, rate, or concurrency is outside the certified bound")
	}
	if p.MaxP95MS < 1 || p.MaxP95MS > 60_000 || p.MaxErrorRate < 0 || p.MaxErrorRate > 0.25 || len(p.Actors) == 0 || len(p.Actors) > 500 || len(p.Routes) == 0 || len(p.Routes) > 20 {
		return errors.New("threshold or workload cardinality is invalid")
	}
	seenActors, actorWeight := map[string]bool{}, 0
	for _, item := range p.Actors {
		if !machineName.MatchString(item.Name) || seenActors[item.Name] || item.Weight < 1 || item.Weight > 100 || (item.CookieEnv != "" && !cookieEnvRE.MatchString(item.CookieEnv)) || (item.AccountIDEnv != "" && !accountEnvRE.MatchString(item.AccountIDEnv)) {
			return errors.New("actor name, weight, or environment reference is invalid")
		}
		seenActors[item.Name], actorWeight = true, actorWeight+item.Weight
	}
	seenRoutes, routeWeight, needsAccountID := map[string]bool{}, 0, false
	for _, item := range p.Routes {
		accountTokens := strings.Count(item.Path, "{account_id}")
		validationPath := strings.ReplaceAll(item.Path, "{account_id}", "00000000-0000-4000-8000-000000000001")
		parsed, err := url.ParseRequestURI(validationPath)
		invalidTemplate := accountTokens > 1 || strings.ContainsAny(strings.ReplaceAll(item.Path, "{account_id}", ""), "{}")
		if !machineName.MatchString(item.Name) || seenRoutes[item.Name] || err != nil || !strings.HasPrefix(item.Path, "/") || parsed.IsAbs() || parsed.Fragment != "" || item.Weight < 1 || item.Weight > 100 || item.Status < 200 || item.Status > 599 || !validMediaType(item.ContentType) {
			return errors.New("route name, path, weight, status, or content type is invalid")
		}
		if invalidTemplate {
			return errors.New("route contains an invalid account identifier template")
		}
		needsAccountID = needsAccountID || accountTokens == 1
		seenRoutes[item.Name], routeWeight = true, routeWeight+item.Weight
	}
	if needsAccountID {
		seenAccountEnvironments := map[string]bool{}
		for _, item := range p.Actors {
			if item.AccountIDEnv == "" || seenAccountEnvironments[item.AccountIDEnv] {
				return fmt.Errorf("actor %s requires an account identifier environment reference", item.Name)
			}
			seenAccountEnvironments[item.AccountIDEnv] = true
		}
	} else {
		for _, item := range p.Actors {
			if item.AccountIDEnv != "" {
				return fmt.Errorf("actor %s has an unused account identifier environment reference", item.Name)
			}
		}
	}
	if actorWeight > 1000 || routeWeight > 1000 || actorWeight*routeWeight > 10_000 || p.RequestsPerSecond*p.DurationSeconds < actorWeight*routeWeight {
		return errors.New("measured request count must cover at least one complete weighted workload cycle")
	}
	return nil
}

func validMediaType(value string) bool {
	return value == "application/json" || value == "text/html" || value == "application/problem+json"
}

func loadCredentials(actors []actor, lookup func(string) (string, bool)) ([]credential, error) {
	result := make([]credential, 0, len(actors))
	seenAccountIDs := map[string]bool{}
	for _, item := range actors {
		cookie := ""
		if item.CookieEnv != "" {
			var ok bool
			cookie, ok = lookup(item.CookieEnv)
			if !ok || cookie == "" || len(cookie) > 8192 || strings.ContainsAny(cookie, "\r\n\x00") {
				return nil, fmt.Errorf("%s is missing or invalid", item.CookieEnv)
			}
		}
		accountID := ""
		if item.AccountIDEnv != "" {
			var ok bool
			accountID, ok = lookup(item.AccountIDEnv)
			if !ok || ids.Validate(accountID) != nil {
				return nil, fmt.Errorf("%s is missing or invalid", item.AccountIDEnv)
			}
			if seenAccountIDs[accountID] {
				return nil, errors.New("synthetic account identifiers must be unique")
			}
			seenAccountIDs[accountID] = true
		}
		result = append(result, credential{actor: item, cookie: cookie, accountID: accountID})
	}
	return result, nil
}

func httpClient(concurrency int) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = concurrency
	transport.MaxIdleConnsPerHost = concurrency
	transport.MaxConnsPerHost = concurrency
	transport.ForceAttemptHTTP2 = true
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("redirect") }}
}

func execute(ctx context.Context, p plan, raw []byte, credentials []credential, client *http.Client, now func() time.Time) report {
	planDigest := sha256.Sum256(raw)
	value := report{SchemaVersion: 1, Environment: p.Environment, Revision: p.Revision, ImageDigest: p.ImageDigest, PlanSHA256: hex.EncodeToString(planDigest[:]), WarmupSeconds: p.WarmupSeconds, DurationSeconds: p.DurationSeconds, RequestsPerSecond: p.RequestsPerSecond, Concurrency: p.Concurrency, MaxP95MS: p.MaxP95MS, MaxErrorRate: p.MaxErrorRate}
	cases := expandCases(credentials, p.Routes)
	if p.WarmupSeconds > 0 {
		_ = runWindow(ctx, time.Duration(p.WarmupSeconds)*time.Second, p.RequestsPerSecond, p.Concurrency, p.Origin, cases, client, nil)
	}
	value.PlannedRequests = p.DurationSeconds * p.RequestsPerSecond
	value.StartedAt = now().UTC()
	observations := make(chan observation, p.Concurrency*2)
	var collected sync.WaitGroup
	collected.Add(1)
	groups := map[string]*samples{}
	go func() {
		defer collected.Done()
		for item := range observations {
			key := item.actor + "\x00" + item.route
			group := groups[key]
			if group == nil {
				group = &samples{statuses: map[string]int{}, codes: map[string]int{}}
				groups[key] = group
			}
			group.durations = append(group.durations, item.durationMS)
			if item.status != 0 {
				group.statuses[fmt.Sprintf("%d", item.status)]++
			}
			if item.errorCode != "" {
				group.errors++
				group.codes[item.errorCode]++
			}
		}
	}()
	value.ScheduledRequests = runWindow(ctx, time.Duration(p.DurationSeconds)*time.Second, p.RequestsPerSecond, p.Concurrency, p.Origin, cases, client, observations)
	close(observations)
	collected.Wait()
	value.CompletedAt = now().UTC()
	value.Results = summarize(p, groups)
	for _, item := range value.Results {
		value.CompletedRequests += item.Samples
	}
	value.Success = value.ScheduledRequests == value.PlannedRequests && value.CompletedRequests == value.PlannedRequests && len(value.Results) == len(p.Actors)*len(p.Routes)
	for _, item := range value.Results {
		value.Success = value.Success && item.Passed
	}
	return value
}

func expandCases(actors []credential, routes []route) []requestCase {
	var result []requestCase
	for _, subject := range actors {
		for range subject.actor.Weight {
			for _, endpoint := range routes {
				for range endpoint.Weight {
					result = append(result, requestCase{actor: subject, route: endpoint})
				}
			}
		}
	}
	return result
}

func runWindow(ctx context.Context, duration time.Duration, rate, concurrency int, origin string, cases []requestCase, client *http.Client, output chan<- observation) int {
	window, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	jobs := make(chan requestCase, concurrency)
	var workers sync.WaitGroup
	for range concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				observed := requestOnce(ctx, origin, item, client)
				if output != nil {
					output <- observed
				}
			}
		}()
	}
	interval := time.Second / time.Duration(rate)
	started := time.Now()
	target := int(duration/time.Second) * rate
	scheduled := 0
schedule:
	for scheduled < target {
		due := started.Add(time.Duration(scheduled) * interval)
		if wait := time.Until(due); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-window.Done():
				timer.Stop()
				break schedule
			case <-timer.C:
			}
		}
		select {
		case jobs <- cases[scheduled%len(cases)]:
			scheduled++
		case <-window.Done():
			break schedule
		}
	}
	close(jobs)
	workers.Wait()
	return scheduled
}

func requestOnce(ctx context.Context, origin string, item requestCase, client *http.Client) observation {
	started := time.Now()
	result := observation{actor: item.actor.actor.Name, route: item.route.Name}
	path := strings.ReplaceAll(item.route.Path, "{account_id}", url.PathEscape(item.actor.accountID))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+path, nil)
	if err == nil {
		request.Header.Set("Accept", item.route.ContentType)
		if item.actor.cookie != "" {
			request.Header.Set("Cookie", item.actor.cookie)
		}
		var response *http.Response
		response, err = client.Do(request)
		if err == nil {
			result.status = response.StatusCode
			body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
			closeErr := response.Body.Close()
			switch {
			case readErr != nil || closeErr != nil:
				result.errorCode = "read"
			case len(body) > maxResponseBody:
				result.errorCode = "body_limit"
			case response.StatusCode != item.route.Status:
				result.errorCode = "status"
			case !matchesMediaType(response.Header.Get("Content-Type"), item.route.ContentType):
				result.errorCode = "content_type"
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

func summarize(p plan, groups map[string]*samples) []result {
	var results []result
	for _, subject := range p.Actors {
		for _, endpoint := range p.Routes {
			group := groups[subject.Name+"\x00"+endpoint.Name]
			if group == nil || len(group.durations) == 0 {
				continue
			}
			sort.Slice(group.durations, func(i, j int) bool { return group.durations[i] < group.durations[j] })
			count := len(group.durations)
			item := result{Actor: subject.Name, Route: endpoint.Name, Samples: count, Errors: group.errors, ErrorRate: float64(group.errors) / float64(count), P50MS: percentile(group.durations, 50), P95MS: percentile(group.durations, 95), P99MS: percentile(group.durations, 99), MaxMS: group.durations[count-1], StatusCodes: group.statuses, ErrorCodes: group.codes}
			item.Passed = item.P95MS <= p.MaxP95MS && item.ErrorRate <= p.MaxErrorRate
			results = append(results, item)
		}
	}
	slices.SortFunc(results, func(a, b result) int {
		if value := strings.Compare(a.Actor, b.Actor); value != 0 {
			return value
		}
		return strings.Compare(a.Route, b.Route)
	})
	return results
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
