package architecture_test

import (
	"os"
	"path/filepath"
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

	checkReleasePublisher(t, readReleaseFile(t, filepath.Join(root, "deploy", "docker", "spyglass", "publish-stage-release.sh")))
}

func TestGitHubActionsAreDisabled(t *testing.T) {
	workflowDirectory := filepath.Join("..", "..", ".github", "workflows")
	entries, err := os.ReadDir(workflowDirectory)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if !entry.IsDir() && (extension == ".yml" || extension == ".yaml") {
			t.Fatalf("GitHub Actions must remain disabled; found %s", entry.Name())
		}
	}
}

func checkReleasePublisher(t *testing.T, publisher string) {
	t.Helper()
	for _, required := range []string{
		"SPYGLASS_RELEASE_PLATFORMS",
		"docker buildx imagetools inspect",
		"--provenance mode=max",
		"--sbom true",
		"aquasec/trivy@sha256:",
		"--scanners vuln,secret",
		"--severity HIGH,CRITICAL",
		"--exit-code 1",
		"SPYGLASS_APPLICATION_IMAGE=",
		"SPYGLASS_WEBSITE_IMAGE=",
		"SPYGLASS_PRIVATE_UI_IMAGE=",
	} {
		if !strings.Contains(publisher, required) {
			t.Fatalf("release publisher is missing %q", required)
		}
	}
	if strings.Contains(publisher, ":latest") {
		t.Fatal("release publisher uses a mutable latest tag")
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
