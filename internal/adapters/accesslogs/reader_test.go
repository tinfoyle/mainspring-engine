package accesslogs

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/trafficreport"
)

func TestReportBoundsListsAndWarnsForMissingCurrentHostLog(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	var data strings.Builder
	for i := 1; i <= 1020; i++ {
		fmt.Fprintf(&data, `{"ts":%d,"request":{"remote_ip":"2001:db8::%x","host":"stage.example.com","method":"GET"},"status":200,"duration":0.01}`+"\n", now.Add(-time.Duration(i)*time.Second).Unix(), i)
	}
	if err := os.WriteFile(filepath.Join(dir, "public.log"), []byte(data.String()), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := New(dir, []string{"stage.example.com", "app.stage.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := reader.Read(context.Background(), trafficreport.Query{From: now.Add(-time.Hour), To: now})
	if err != nil {
		t.Fatal(err)
	}
	if report.Requests != 1020 || report.UniqueIPs != 1020 || len(report.IPs) != 1000 || len(report.Logs) != 200 || !report.IPListTruncated || !report.Truncated {
		t.Fatalf("limits or missing-host warning failed: requests=%d unique=%d IPs=%d logs=%d list=%v partial=%v", report.Requests, report.UniqueIPs, len(report.IPs), len(report.Logs), report.IPListTruncated, report.Truncated)
	}
	if !report.Logs[0].Time.Equal(now.Add(-time.Second)) || !report.Logs[199].Time.Equal(now.Add(-200*time.Second)) {
		t.Fatal("latest 200 requests not retained")
	}
}

func TestReportCountsTrustedPeersAndBoundsReturnedData(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	line := func(ip, host string, status int, at time.Time) string {
		value, _ := json.Marshal(map[string]any{"ts": float64(at.Unix()), "request": map[string]any{"remote_ip": ip, "client_ip": "203.0.113.99", "host": host, "method": "GET", "uri": "/private?token=DO-NOT-RETURN", "headers": map[string]string{"Authorization": "DO-NOT-RETURN"}}, "status": status, "duration": 0.012})
		return string(value) + "\n"
	}
	current := line("::ffff:192.0.2.1", "stage.example.com", 200, now.Add(-time.Minute)) + line("192.0.2.2", "stage.example.com", 503, now.Add(-2*time.Minute)) + line("192.0.2.3", "stage.example.com", 200, now) // exclusive upper bound
	if err := os.WriteFile(filepath.Join(dir, "public.log"), []byte(current), 0600); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Create(filepath.Join(dir, "public-2026-09-04T12-00-00.000.log.gz"))
	gz := gzip.NewWriter(f)
	_, _ = gz.Write([]byte(line("192.0.2.1", "stage.example.com", 200, now.Add(-time.Hour)) + line("192.0.2.9", "unrelated.example.com", 200, now.Add(-time.Hour)) + "invalid\n"))
	gz.Close()
	f.Close()
	outside := filepath.Join(t.TempDir(), "private.log")
	_ = os.WriteFile(outside, []byte(line("192.0.2.8", "stage.example.com", 200, now.Add(-time.Minute))), 0600)
	if err := os.Symlink(outside, filepath.Join(dir, "ops.log")); err != nil {
		t.Fatal(err)
	}
	r, err := New(dir, []string{"stage.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := r.Read(context.Background(), trafficreport.Query{From: now.Add(-2 * time.Hour), To: now})
	if err != nil {
		t.Fatal(err)
	}
	if report.Requests != 3 || report.UniqueIPs != 2 || report.ServerErrors != 1 || report.FilesRead != 2 || report.InvalidRecords != 2 || len(report.Logs) != 3 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.IPs[0].IP != "192.0.2.1" || report.IPs[0].Requests != 2 {
		t.Fatalf("IP normalization/counts: %+v", report.IPs)
	}
	encoded, _ := json.Marshal(report)
	for _, secret := range []string{"DO-NOT-RETURN", "203.0.113.99", "192.0.2.8", "unrelated.example.com"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("report leaked excluded input", secret)
		}
	}
	if !report.Logs[0].Time.After(report.Logs[1].Time) {
		t.Fatal("logs are not newest first")
	}
}

func TestMissingLogsAndCanceledReadNeverLookLikeZeroTraffic(t *testing.T) {
	r, err := New(t.TempDir(), []string{"stage.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(context.Background(), trafficreport.Query{}); err != trafficreport.ErrUnavailable {
		t.Fatalf("empty directory: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = os.WriteFile(filepath.Join(r.directory, "app.log"), []byte("{}\n"), 0600)
	if _, err := r.Read(ctx, trafficreport.Query{}); err != trafficreport.ErrUnavailable {
		t.Fatalf("canceled: %v", err)
	}
}

func TestUserAgentsAreOptionalBoundedText(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	agents := []string{"Mozilla/5.0 Firefox/142.0", "", "<script>alert(1)</script>\r\n\t\u202efake", strings.Repeat("界", 1100)}
	var data strings.Builder
	for i, agent := range agents {
		row := map[string]any{"ts": now.Add(-time.Duration(i+1) * time.Second).Unix(), "request": map[string]any{"remote_ip": "192.0.2.1", "host": "stage.example.com", "method": "GET"}, "status": 200, "duration": 0.01}
		if agent != "" {
			row["user_agent"] = agent
		} // Legacy records omit the field.
		value, _ := json.Marshal(row)
		data.Write(value)
		data.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "public.log"), []byte(data.String()), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := New(dir, []string{"stage.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := reader.Read(context.Background(), trafficreport.Query{From: now.Add(-time.Hour), To: now})
	if err != nil {
		t.Fatal(err)
	}
	if report.Requests != 4 || report.InvalidRecords != 0 || report.Truncated {
		t.Fatalf("unexpected report: %+v", report)
	}
	expected := []string{agents[0], "", "<script>alert(1)</script>fake", strings.Repeat("界", 1023) + "…"}
	for i, agent := range expected {
		if report.Logs[i].UserAgent != agent {
			t.Errorf("user agent %d was not preserved/sanitized correctly", i)
		}
	}
	encoded, err := json.Marshal(report.Logs[1])
	if err != nil || !strings.Contains(string(encoded), `"user_agent":""`) {
		t.Fatal("missing user agents must remain explicit in the API")
	}
}
