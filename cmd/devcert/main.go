// Command devcert creates short-lived, local-only workload identities for the
// secure Docker verification topology. It is a source-tree development tool;
// the release image contains only the spyglass binary.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type identity struct {
	name     string
	dnsNames []string
	uri      string
	usage    []x509.ExtKeyUsage
}

func main() {
	output := flag.String("output", "/out", "directory for generated local workload identities")
	flag.Parse()
	if flag.NArg() != 0 {
		fatal(errors.New("devcert accepts only --output"))
	}
	if err := generate(*output, time.Now().UTC()); err != nil {
		fatal(err)
	}
}

func generate(output string, now time.Time) error {
	if output == "" || !filepath.IsAbs(output) {
		return errors.New("devcert output must be an absolute path")
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "Spyglass secure-local workload CA", Organization: []string{"Infinite Ocean local verification"}},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create CA: %w", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	identities := []identity{
		{name: "app-router", uri: "spiffe://infiniteocean.net/spyglass/workloads/app-router", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "mcp-gateway", uri: "spiffe://infiniteocean.net/spyglass/workloads/mcp-gateway", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "app-api-a", dnsNames: []string{"app-api-a"}, uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/app-api", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}},
		{name: "app-api-b", dnsNames: []string{"app-api-b"}, uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/app-api", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}},
		{name: "admission-api", dnsNames: []string{"admission-api"}, uri: "spiffe://infiniteocean.net/spyglass/workloads/admission-api", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{name: "agent-dispatch-worker-a", uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/agent-dispatch-worker", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "agent-dispatch-worker-b", uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/agent-dispatch-worker", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "agent-projection-worker-a", uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/agent-projection-worker", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "agent-projection-worker-b", uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/agent-projection-worker", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "schedule-execution-worker-a", uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/schedule-execution-worker", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "schedule-execution-worker-b", uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/schedule-execution-worker", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "tool-router", dnsNames: []string{"tool-router"}, uri: "spiffe://infiniteocean.net/spyglass/workloads/tool-router", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{name: "model-gateway", dnsNames: []string{"model-gateway"}, uri: "spiffe://infiniteocean.net/spyglass/workloads/model-gateway", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{name: "runner-controller-a", uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/runner-controller", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "runner-controller-b", uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/runner-controller", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "runner-broker-a", dnsNames: []string{"runner-broker-a"}, uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/runner-broker", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}},
		{name: "runner-broker-b", dnsNames: []string{"runner-broker-b"}, uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/runner-broker", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}},
		{name: "docker-runner-launcher-a", dnsNames: []string{"docker-runner-launcher-a"}, uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-east-01/docker-runner-launcher", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{name: "docker-runner-launcher-b", dnsNames: []string{"docker-runner-launcher-b"}, uri: "spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/docker-runner-launcher", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{name: "docker-runner-launcher", dnsNames: []string{"docker-runner-launcher"}, uri: "spiffe://infiniteocean.net/spyglass/workloads/docker-runner-launcher", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{name: "runner-controller", uri: "spiffe://infiniteocean.net/spyglass/workloads/runner-controller", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "runner-broker", uri: "spiffe://infiniteocean.net/spyglass/workloads/runner-broker", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
	}
	for _, workload := range identities {
		if err := issue(output, workload, now, caTemplate, caKey, caPEM); err != nil {
			return err
		}
	}
	return nil
}

func issue(output string, workload identity, now time.Time, ca *x509.Certificate, caKey *ecdsa.PrivateKey, caPEM []byte) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate %s key: %w", workload.name, err)
	}
	identityURI, err := url.Parse(workload.uri)
	if err != nil {
		return fmt.Errorf("parse %s identity: %w", workload.name, err)
	}
	template := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: workload.name, Organization: []string{"Infinite Ocean local verification"}},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(12 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  workload.usage,
		DNSNames:     workload.dnsNames,
		URIs:         []*url.URL{identityURI},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create %s certificate: %w", workload.name, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal %s key: %w", workload.name, err)
	}
	directory := filepath.Join(output, workload.name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create %s directory: %w", workload.name, err)
	}
	files := []struct {
		name string
		body []byte
	}{
		{name: "ca.crt", body: caPEM},
		{name: "tls.crt", body: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})},
		{name: "tls.key", body: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})},
	}
	for _, file := range files {
		path := filepath.Join(directory, file.name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("rotate %s %s: %w", workload.name, file.name, err)
		}
		if err := os.WriteFile(path, file.body, 0o444); err != nil {
			return fmt.Errorf("write %s %s: %w", workload.name, file.name, err)
		}
	}
	return nil
}

func randomSerial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		panic(err)
	}
	return serial
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
