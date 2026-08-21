// journey-cert validates and combines content-free observations from one
// reviewed synthetic product-journey plan. It never receives fixture values.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

const maximumInputBytes = 1 << 20

var (
	machinePattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{1,47}$`)
	revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	failurePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

	validKinds = map[string]bool{
		"anonymous-boundary": true, "registration-email": true,
		"passkey-enrollment": true, "passkey-login": true,
		"membership": true, "account-isolation": true,
		"stripe-checkout": true, "stripe-webhook": true,
		"billing-projection": true, "identity-recovery": true,
		"provider-degradation": true, "container-recovery": true,
		"database-recovery": true, "runner-recovery": true,
		"accessibility": true, "cleanup": true,
	}
	validExecutions = map[string]bool{
		"automated": true, "browser": true, "assistive-technology": true,
		"operator": true,
	}
)

type plan struct {
	SchemaVersion          int            `json:"schema_version"`
	Profile                string         `json:"profile"`
	Environment            string         `json:"environment"`
	Revision               string         `json:"revision"`
	ApplicationImageDigest string         `json:"application_image_digest"`
	WebsiteImageDigest     string         `json:"website_image_digest"`
	WebsiteOrigin          string         `json:"website_origin"`
	ApplicationOrigin      string         `json:"application_origin"`
	CatalogVersion         uint64         `json:"catalog_version"`
	RequiredOffers         []string       `json:"required_offers"`
	FixtureInventorySHA256 string         `json:"fixture_inventory_sha256"`
	Checks                 []plannedCheck `json:"checks"`
}

type plannedCheck struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Execution string `json:"execution"`
	Required  bool   `json:"required"`
}

type observations struct {
	SchemaVersion int             `json:"schema_version"`
	PlanSHA256    string          `json:"plan_sha256"`
	StartedAt     time.Time       `json:"started_at"`
	CompletedAt   time.Time       `json:"completed_at"`
	Checks        []observedCheck `json:"checks"`
}

type observedCheck struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	DurationMS  int64      `json:"duration_ms"`
	FailureCode string     `json:"failure_code,omitempty"`
	Artifacts   []artifact `json:"artifacts,omitempty"`
}

type artifact struct {
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

type report struct {
	SchemaVersion          int           `json:"schema_version"`
	Profile                string        `json:"profile"`
	Environment            string        `json:"environment"`
	Revision               string        `json:"revision"`
	ApplicationImageDigest string        `json:"application_image_digest"`
	WebsiteImageDigest     string        `json:"website_image_digest"`
	PlanSHA256             string        `json:"plan_sha256"`
	FixtureInventorySHA256 string        `json:"fixture_inventory_sha256"`
	CatalogVersion         uint64        `json:"catalog_version"`
	StartedAt              time.Time     `json:"started_at"`
	CompletedAt            time.Time     `json:"completed_at"`
	Success                bool          `json:"success"`
	Checks                 []reportCheck `json:"checks"`
}

type reportCheck struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Execution   string     `json:"execution"`
	Required    bool       `json:"required"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	DurationMS  int64      `json:"duration_ms"`
	FailureCode string     `json:"failure_code,omitempty"`
	Artifacts   []artifact `json:"artifacts,omitempty"`
}

func main() {
	planPath := flag.String("plan", "", "reviewed product-journey plan JSON")
	observationsPath := flag.String("observations", "", "mode-600 content-free observations JSON")
	output := flag.String("output", "-", "new evidence JSON file, or - for stdout")
	flag.Parse()
	if flag.NArg() != 0 || *planPath == "" || *observationsPath == "" {
		fmt.Fprintln(os.Stderr, "usage: journey-cert -plan <plan.json> -observations <observations.json> -output <new-evidence.json>")
		os.Exit(2)
	}
	value, rawPlan, err := readPlan(*planPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plan:", err)
		os.Exit(2)
	}
	run, err := readObservations(*observationsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "observations:", err)
		os.Exit(2)
	}
	evidence, err := combine(value, rawPlan, run)
	if err != nil {
		fmt.Fprintln(os.Stderr, "certification:", err)
		os.Exit(2)
	}
	if err := writeReport(*output, evidence); err != nil {
		fmt.Fprintln(os.Stderr, "write evidence:", err)
		os.Exit(2)
	}
	if !evidence.Success {
		os.Exit(1)
	}
}

func readPlan(path string) (plan, []byte, error) {
	raw, err := readBounded(path)
	if err != nil {
		return plan{}, nil, err
	}
	var value plan
	if err := decodeStrict(raw, &value); err != nil {
		return plan{}, nil, err
	}
	if err := value.validate(); err != nil {
		return plan{}, nil, err
	}
	return value, raw, nil
}

func readObservations(path string) (observations, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return observations{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return observations{}, errors.New("observations must be a regular file readable only by its owner")
	}
	raw, err := readBounded(path)
	if err != nil {
		return observations{}, err
	}
	var value observations
	if err := decodeStrict(raw, &value); err != nil {
		return observations{}, err
	}
	if err := value.validate(); err != nil {
		return observations{}, err
	}
	return value, nil
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maximumInputBytes {
		return nil, errors.New("input exceeds 1 MiB")
	}
	return raw, nil
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("input must contain exactly one JSON value")
	}
	return nil
}

func (p plan) validate() error {
	if p.SchemaVersion != 1 || (p.Profile != "package-slice" && p.Profile != "phase3-product") {
		return errors.New("schema version or profile is invalid")
	}
	if !machinePattern.MatchString(p.Environment) || !revisionPattern.MatchString(p.Revision) || !digestPattern.MatchString(p.ApplicationImageDigest) || !digestPattern.MatchString(p.WebsiteImageDigest) || !digestPattern.MatchString(p.FixtureInventorySHA256) {
		return errors.New("environment, release identity, or fixture inventory digest is invalid")
	}
	if p.ApplicationImageDigest == p.WebsiteImageDigest || p.CatalogVersion == 0 {
		return errors.New("application and website identities must be distinct and Catalog version must be positive")
	}
	if err := exactHTTPSOrigin(p.WebsiteOrigin); err != nil {
		return fmt.Errorf("website origin: %w", err)
	}
	if err := exactHTTPSOrigin(p.ApplicationOrigin); err != nil {
		return fmt.Errorf("application origin: %w", err)
	}
	if p.WebsiteOrigin == p.ApplicationOrigin {
		return errors.New("website and application origins must be distinct")
	}
	if len(p.RequiredOffers) == 0 || len(p.RequiredOffers) > 20 || len(p.Checks) == 0 || len(p.Checks) > 100 {
		return errors.New("offers or check cardinality is invalid")
	}
	seenOffers := map[string]bool{}
	for _, offer := range p.RequiredOffers {
		if !machinePattern.MatchString(offer) || seenOffers[offer] {
			return errors.New("required offers must be unique machine names")
		}
		seenOffers[offer] = true
	}
	seenIDs, seenKinds, requiredChecks := map[string]bool{}, map[string]bool{}, 0
	for _, item := range p.Checks {
		if !machinePattern.MatchString(item.ID) || seenIDs[item.ID] || !validKinds[item.Kind] || !validExecutions[item.Execution] {
			return errors.New("check identity, kind, or execution is invalid")
		}
		seenIDs[item.ID], seenKinds[item.Kind] = true, true
		if item.Required {
			requiredChecks++
		}
	}
	if requiredChecks == 0 {
		return errors.New("plan must contain at least one required check")
	}
	if p.Profile == "phase3-product" {
		for kind := range validKinds {
			if !seenKinds[kind] {
				return fmt.Errorf("phase3-product plan is missing %s", kind)
			}
		}
		for _, item := range p.Checks {
			if !item.Required {
				return errors.New("every phase3-product check must be required")
			}
		}
	}
	return nil
}

func exactHTTPSOrigin(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != raw {
		return errors.New("must be an exact HTTPS origin")
	}
	return nil
}

func (o observations) validate() error {
	if o.SchemaVersion != 1 || !digestPattern.MatchString(o.PlanSHA256) || o.StartedAt.IsZero() || o.CompletedAt.IsZero() || o.StartedAt.Location() != time.UTC || o.CompletedAt.Location() != time.UTC || o.CompletedAt.Before(o.StartedAt) || o.CompletedAt.Sub(o.StartedAt) > 72*time.Hour {
		return errors.New("schema, plan digest, or UTC evidence window is invalid")
	}
	if len(o.Checks) == 0 || len(o.Checks) > 100 {
		return errors.New("observation cardinality is invalid")
	}
	seen := map[string]bool{}
	for _, item := range o.Checks {
		if !machinePattern.MatchString(item.ID) || seen[item.ID] || (item.Status != "passed" && item.Status != "failed" && item.Status != "blocked") || item.Attempts < 1 || item.Attempts > 20 || item.DurationMS < 0 || item.DurationMS > int64((72*time.Hour)/time.Millisecond) || len(item.Artifacts) > 16 {
			return errors.New("observation identity, status, attempts, duration, or artifact count is invalid")
		}
		seen[item.ID] = true
		if item.Status == "passed" {
			if item.FailureCode != "" || len(item.Artifacts) == 0 {
				return errors.New("passed observations require artifacts and no failure code")
			}
		} else if !failurePattern.MatchString(item.FailureCode) {
			return errors.New("failed or blocked observations require a bounded failure code")
		}
		artifactKinds := map[string]bool{}
		for _, value := range item.Artifacts {
			if !machinePattern.MatchString(value.Kind) || artifactKinds[value.Kind] || !digestPattern.MatchString(value.SHA256) {
				return errors.New("artifact kind or digest is invalid")
			}
			artifactKinds[value.Kind] = true
		}
	}
	return nil
}

func combine(p plan, rawPlan []byte, run observations) (report, error) {
	planSum := sha256.Sum256(rawPlan)
	planDigest := "sha256:" + hex.EncodeToString(planSum[:])
	if run.PlanSHA256 != planDigest {
		return report{}, errors.New("observation plan digest does not match the reviewed plan")
	}
	byID := make(map[string]observedCheck, len(run.Checks))
	for _, item := range run.Checks {
		byID[item.ID] = item
	}
	if len(byID) != len(p.Checks) {
		return report{}, errors.New("observations must cover the plan exactly")
	}
	result := report{SchemaVersion: 1, Profile: p.Profile, Environment: p.Environment, Revision: p.Revision, ApplicationImageDigest: p.ApplicationImageDigest, WebsiteImageDigest: p.WebsiteImageDigest, PlanSHA256: planDigest, FixtureInventorySHA256: p.FixtureInventorySHA256, CatalogVersion: p.CatalogVersion, StartedAt: run.StartedAt.UTC(), CompletedAt: run.CompletedAt.UTC(), Success: true}
	for _, planned := range p.Checks {
		observed, ok := byID[planned.ID]
		if !ok {
			return report{}, fmt.Errorf("observation for %s is missing", planned.ID)
		}
		delete(byID, planned.ID)
		item := reportCheck{ID: planned.ID, Kind: planned.Kind, Execution: planned.Execution, Required: planned.Required, Status: observed.Status, Attempts: observed.Attempts, DurationMS: observed.DurationMS, FailureCode: observed.FailureCode, Artifacts: slices.Clone(observed.Artifacts)}
		result.Checks = append(result.Checks, item)
		if planned.Required && observed.Status != "passed" {
			result.Success = false
		}
	}
	if len(byID) != 0 {
		return report{}, errors.New("observations contain a check not present in the plan")
	}
	return result, nil
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
