package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateCreatesBoundedWorkloadIdentities(t *testing.T) {
	output := t.TempDir()
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	if err := generate(output, now); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		dnsName  string
		identity string
	}{
		{name: "app-router", identity: "spiffe://infiniteocean.net/spyglass/workloads/app-router"},
		{name: "mcp-gateway", identity: "spiffe://infiniteocean.net/spyglass/workloads/mcp-gateway"},
		{name: "app-api-a", dnsName: "app-api-a", identity: "spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/app-api"},
		{name: "app-api-b", dnsName: "app-api-b", identity: "spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/app-api"},
		{name: "admission-api", dnsName: "admission-api", identity: "spiffe://infiniteocean.net/spyglass/workloads/admission-api"},
		{name: "agent-dispatch-worker-a", identity: "spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/agent-dispatch-worker"},
		{name: "agent-dispatch-worker-b", identity: "spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/agent-dispatch-worker"},
		{name: "schedule-execution-worker-a", identity: "spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/schedule-execution-worker"},
		{name: "schedule-execution-worker-b", identity: "spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/schedule-execution-worker"},
		{name: "docker-runner-launcher", dnsName: "docker-runner-launcher", identity: "spiffe://infiniteocean.net/spyglass/workloads/docker-runner-launcher"},
		{name: "runner-controller", identity: "spiffe://infiniteocean.net/spyglass/workloads/runner-controller"},
		{name: "runner-broker", identity: "spiffe://infiniteocean.net/spyglass/workloads/runner-broker"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pair, err := tls.LoadX509KeyPair(filepath.Join(output, test.name, "tls.crt"), filepath.Join(output, test.name, "tls.key"))
			if err != nil {
				t.Fatal(err)
			}
			certificate, err := x509.ParseCertificate(pair.Certificate[0])
			if err != nil {
				t.Fatal(err)
			}
			if len(certificate.URIs) != 1 || certificate.URIs[0].String() != test.identity {
				t.Fatalf("identity=%v", certificate.URIs)
			}
			if test.dnsName != "" {
				roots := x509.NewCertPool()
				caPEM, readErr := os.ReadFile(filepath.Join(output, test.name, "ca.crt"))
				if readErr != nil || !roots.AppendCertsFromPEM(caPEM) {
					t.Fatal("load generated CA")
				}
				if _, verifyErr := certificate.Verify(x509.VerifyOptions{Roots: roots, DNSName: test.dnsName, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, CurrentTime: now}); verifyErr != nil {
					t.Fatal(verifyErr)
				}
			}
			contents, readErr := os.ReadFile(filepath.Join(output, test.name, "tls.crt"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			block, _ := pem.Decode(contents)
			if block == nil || block.Type != "CERTIFICATE" || certificate.NotAfter.After(now.Add(13*time.Hour)) {
				t.Fatal("certificate lifetime is not bounded")
			}
		})
	}
}
