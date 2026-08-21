package architecture_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseImageContract(t *testing.T) {
	root := filepath.Join("..", "..")
	dockerfile := readReleaseFile(t, filepath.Join(root, "Dockerfile"))
	for _, required := range []string{
		"# syntax=docker/dockerfile:1.7@sha256:",
		"golang:1.26.6-alpine3.23@sha256:",
		"CGO_ENABLED=0",
		"-trimpath",
		"internal/platform/buildinfo.Version",
		"FROM scratch",
		"USER 65532:65532",
		`ENTRYPOINT ["/spyglass"]`,
	} {
		if !strings.Contains(dockerfile, required) {
			t.Fatalf("Dockerfile is missing release invariant %q", required)
		}
	}
	if strings.Contains(dockerfile, "COPY . ") || strings.Contains(dockerfile, ":latest") {
		t.Fatal("release image uses an unbounded context copy or mutable latest tag")
	}

	ignore := readReleaseFile(t, filepath.Join(root, ".dockerignore"))
	if !strings.HasPrefix(ignore, "*\n") || strings.Contains(ignore, "prototype") || strings.Contains(ignore, "github-token") {
		t.Fatal("Docker context is not allowlist-only")
	}

	checkReleaseWorkflow(t, readReleaseFile(t, filepath.Join(root, ".github", "workflows", "release-image.yml")))
	checkReleaseWorkflow(t, readReleaseFile(t, filepath.Join(root, ".github", "workflows", "release-website-image.yml")))
}

func TestVerificationWorkflowContract(t *testing.T) {
	workflow := readReleaseFile(t, filepath.Join("..", "..", ".github", "workflows", "verify.yml"))
	checkPinnedActions(t, workflow)
	for _, required := range []string{
		"postgres:17.11-alpine3.24@sha256:",
		"bash deploy/kubernetes/overlays/verify.sh",
		"bash deploy/docker/spyglass/test-stage-contract.sh",
		"aquasec/trivy@sha256:",
		"--scanners vuln,secret",
		"--severity HIGH,CRITICAL",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("verification workflow is missing %q", required)
		}
	}
}

func checkReleaseWorkflow(t *testing.T, workflow string) {
	t.Helper()
	for _, required := range []string{"linux/amd64,linux/arm64", "provenance: mode=max", "sbom: true", "id-token: write", "cosign sign --yes", "environment: release", "refusing to overwrite existing image tag", "aquasec/trivy@sha256:", "--scanners vuln,secret", "--severity HIGH,CRITICAL", "--exit-code 1"} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("release workflow is missing %q", required)
		}
	}
	if strings.Contains(workflow, ":latest") {
		t.Fatal("release workflow publishes a mutable latest tag")
	}
	if strings.Contains(workflow, "actions/attest@") {
		t.Fatal("release workflow uses GitHub artifact attestations, which are unavailable to this user-owned private repository")
	}
	checkPinnedActions(t, workflow)
}

func checkPinnedActions(t *testing.T, workflow string) {
	t.Helper()
	action := regexp.MustCompile(`uses:\s+[^\s@]+@([^\s]+)`)
	matches := action.FindAllStringSubmatch(workflow, -1)
	if len(matches) == 0 {
		t.Fatal("release workflow has no actions")
	}
	sha := regexp.MustCompile(`^[0-9a-f]{40}$`)
	for _, match := range matches {
		if !sha.MatchString(match[1]) {
			t.Fatalf("release action is not commit-pinned: %q", match[0])
		}
	}
}

func readReleaseFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}
