package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const healthcheckMaximumResponseBytes = 64 << 10

func runHealthcheck(arguments []string) error {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	target := flags.String("url", "", "exact HTTP(S) readiness URL")
	caFile := flags.String("ca-file", "", "PEM trust bundle for HTTPS")
	certificateFile := flags.String("cert-file", "", "PEM client certificate for HTTPS")
	keyFile := flags.String("key-file", "", "PEM client private key for HTTPS")
	timeout := flags.Duration("timeout", 3*time.Second, "request timeout")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
		return errors.New("healthcheck arguments are invalid")
	}
	parsed, err := url.Parse(*target)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return errors.New("healthcheck URL must be an exact HTTP(S) URL without credentials, query, or fragment")
	}
	if *timeout <= 0 || *timeout > 30*time.Second {
		return errors.New("healthcheck timeout must be between 1ns and 30s")
	}
	if (*certificateFile == "") != (*keyFile == "") {
		return errors.New("healthcheck client certificate and key must be supplied together")
	}
	if parsed.Scheme == "http" && (*caFile != "" || *certificateFile != "") {
		return errors.New("healthcheck TLS files require an HTTPS URL")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	if parsed.Scheme == "https" {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		if *caFile != "" {
			contents, readErr := os.ReadFile(*caFile)
			if readErr != nil {
				return fmt.Errorf("read healthcheck CA file: %w", readErr)
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(contents) {
				return errors.New("healthcheck CA file contains no certificates")
			}
			tlsConfig.RootCAs = roots
		}
		if *certificateFile != "" {
			certificate, loadErr := tls.LoadX509KeyPair(*certificateFile, *keyFile)
			if loadErr != nil {
				return fmt.Errorf("load healthcheck client identity: %w", loadErr)
			}
			tlsConfig.Certificates = []tls.Certificate{certificate}
		}
		transport.TLSClientConfig = tlsConfig
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   *timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("healthcheck redirects are denied")
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return fmt.Errorf("create healthcheck request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request health endpoint: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	if contentLength := response.ContentLength; contentLength > healthcheckMaximumResponseBytes {
		return errors.New("health endpoint response is too large")
	}
	limited := io.LimitReader(response.Body, healthcheckMaximumResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read health endpoint response: %w", err)
	}
	if len(body) > healthcheckMaximumResponseBytes {
		return errors.New("health endpoint response is too large")
	}
	if strings.TrimSpace(string(body)) == "" {
		return errors.New("health endpoint returned an empty response")
	}
	return nil
}
