package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCombinesCompleteProductEvidenceWithoutFixtureContent(t *testing.T) {
	p := validPlan("phase3-product")
	raw, _ := json.Marshal(p)
	run := validObservations(p, raw)
	result, err := combine(p, raw, run)
	if err != nil || !result.Success || len(result.Checks) != len(validKinds) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"synthetic@example.com", "__Host-session", "credential-private-key", "cus_", "sub_", "price_"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("portable evidence leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestProductPlanRequiresEveryFinalJourney(t *testing.T) {
	p := validPlan("phase3-product")
	p.Checks = p.Checks[:len(p.Checks)-1]
	if err := p.validate(); err == nil {
		t.Fatal("incomplete final-product plan was accepted")
	}
	p = validPlan("package-slice")
	p.Checks = p.Checks[:1]
	if err := p.validate(); err != nil {
		t.Fatalf("bounded package slice was rejected: %v", err)
	}
	p.Checks[0].Required = false
	if err := p.validate(); err == nil {
		t.Fatal("package slice without a required check was accepted")
	}
}

func TestStrictPlanAndObservationValidation(t *testing.T) {
	directory := t.TempDir()
	planPath := filepath.Join(directory, "plan.json")
	if err := os.WriteFile(planPath, []byte(`{"schema_version":1,"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPlan(planPath); err == nil {
		t.Fatal("unknown plan field was accepted")
	}

	p := validPlan("package-slice")
	raw, _ := json.Marshal(p)
	run := validObservations(p, raw)
	run.Checks[0].Artifacts = nil
	if err := run.validate(); err == nil {
		t.Fatal("passed observation without artifact evidence was accepted")
	}
	run = validObservations(p, raw)
	run.Checks[0].Status, run.Checks[0].FailureCode = "blocked", ""
	if err := run.validate(); err == nil {
		t.Fatal("blocked observation without a bounded failure code was accepted")
	}
}

func TestPlanBindingAndExactCoverage(t *testing.T) {
	p := validPlan("package-slice")
	raw, _ := json.Marshal(p)
	run := validObservations(p, raw)
	run.PlanSHA256 = "sha256:" + strings.Repeat("a", 64)
	if _, err := combine(p, raw, run); err == nil {
		t.Fatal("observations for another plan were accepted")
	}
	run = validObservations(p, raw)
	run.Checks = run.Checks[:len(run.Checks)-1]
	if _, err := combine(p, raw, run); err == nil {
		t.Fatal("partial observations were accepted")
	}
}

func TestOnlyRequiredFailuresFailPackageSlice(t *testing.T) {
	p := validPlan("package-slice")
	p.Checks = p.Checks[:2]
	p.Checks[1].Required = false
	raw, _ := json.Marshal(p)
	run := validObservations(p, raw)
	run.Checks[1].Status, run.Checks[1].FailureCode, run.Checks[1].Artifacts = "failed", "assertion_failed", nil
	result, err := combine(p, raw, run)
	if err != nil || !result.Success {
		t.Fatalf("optional failure invalidated package slice: %+v err=%v", result, err)
	}
	run.Checks[0].Status, run.Checks[0].FailureCode, run.Checks[0].Artifacts = "failed", "assertion_failed", nil
	result, err = combine(p, raw, run)
	if err != nil || result.Success {
		t.Fatalf("required failure was accepted: %+v err=%v", result, err)
	}
}

func TestObservationFileAndReportPermissions(t *testing.T) {
	directory := t.TempDir()
	p := validPlan("package-slice")
	rawPlan, _ := json.Marshal(p)
	run := validObservations(p, rawPlan)
	rawRun, _ := json.Marshal(run)
	observationPath := filepath.Join(directory, "observations.json")
	if err := os.WriteFile(observationPath, rawRun, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readObservations(observationPath); err == nil {
		t.Fatal("group/world-readable observations were accepted")
	}
	if err := os.Chmod(observationPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readObservations(observationPath); err != nil {
		t.Fatalf("protected observations rejected: %v", err)
	}
	symlinkPath := filepath.Join(directory, "observations-link.json")
	if err := os.Symlink(observationPath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := readObservations(symlinkPath); err == nil {
		t.Fatal("symlinked observations were accepted")
	}
	run.StartedAt = time.Date(2026, 8, 21, 13, 0, 0, 0, time.FixedZone("not-utc", 3600))
	if err := run.validate(); err == nil {
		t.Fatal("non-UTC evidence time was accepted")
	}

	result, err := combine(p, rawPlan, run)
	if err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(directory, "report.json")
	if err := writeReport(reportPath, result); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(reportPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("report permissions=%v err=%v", info, err)
	}
	if err := writeReport(reportPath, result); err == nil {
		t.Fatal("existing evidence was overwritten")
	}
}

func validPlan(profile string) plan {
	p := plan{SchemaVersion: 1, Profile: profile, Environment: "staging-example", Revision: strings.Repeat("1", 40), ApplicationImageDigest: "sha256:" + strings.Repeat("2", 64), WebsiteImageDigest: "sha256:" + strings.Repeat("3", 64), WebsiteOrigin: "https://www.infiniteocean.example", ApplicationOrigin: "https://app.infiniteocean.example", CatalogVersion: 3, RequiredOffers: []string{"free-v1", "team-monthly-v1"}, FixtureInventorySHA256: "sha256:" + strings.Repeat("4", 64)}
	for kind := range validKinds {
		p.Checks = append(p.Checks, plannedCheck{ID: kind, Kind: kind, Execution: "automated", Required: true})
	}
	return p
}

func validObservations(p plan, rawPlan []byte) observations {
	sum := sha256.Sum256(rawPlan)
	value := observations{SchemaVersion: 1, PlanSHA256: "sha256:" + hex.EncodeToString(sum[:]), StartedAt: time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC), CompletedAt: time.Date(2026, 8, 21, 12, 10, 0, 0, time.UTC)}
	for _, item := range p.Checks {
		value.Checks = append(value.Checks, observedCheck{ID: item.ID, Status: "passed", Attempts: 1, DurationMS: 100, Artifacts: []artifact{{Kind: "runner-report", SHA256: "sha256:" + strings.Repeat("5", 64)}}})
	}
	return value
}
