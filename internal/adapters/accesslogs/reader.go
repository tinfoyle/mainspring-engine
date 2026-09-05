// Package accesslogs reads only the content-minimized Stage edge log files.
package accesslogs

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/trafficreport"
)

const maxReadBytes int64 = 128 << 20
const maxUniqueIPs = 100000

var logName = regexp.MustCompile(`^(public|app|mcp|ops)(-[0-9T:._-]+(?:-size|-time)?)?\.log(?:\.gz)?$`)

type Reader struct {
	directory string
	hosts     map[string]bool
}

func New(directory string, hosts []string) (*Reader, error) {
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() || len(hosts) == 0 {
		return nil, trafficreport.ErrUnavailable
	}
	allowed := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		if host == "" || strings.ContainsAny(host, "/: ") {
			return nil, trafficreport.ErrUnavailable
		}
		allowed[host] = true
	}
	return &Reader{directory: directory, hosts: allowed}, nil
}

type record struct {
	Timestamp float64 `json:"ts"`
	Request   struct {
		RemoteIP string `json:"remote_ip"`
		Host     string `json:"host"`
		Method   string `json:"method"`
	} `json:"request"`
	Status   int     `json:"status"`
	Duration float64 `json:"duration"`
}

func (r *Reader) Read(ctx context.Context, query trafficreport.Query) (trafficreport.Report, error) {
	result := trafficreport.Report{From: query.From.UTC(), To: query.To.UTC(), GeneratedAt: time.Now().UTC(), IPs: []trafficreport.IPCount{}, Hosts: []trafficreport.Count{}, Statuses: []trafficreport.Count{}, Days: []trafficreport.Count{}, Logs: []trafficreport.Entry{}}
	root, err := os.OpenRoot(r.directory)
	if err != nil {
		return result, trafficreport.ErrUnavailable
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return result, trafficreport.ErrUnavailable
	}
	files, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return result, trafficreport.ErrUnavailable
	}
	// Current files first, then newest rotations. A bounded result must favor recent data.
	sort.Slice(files, func(i, j int) bool {
		a, b := strings.Count(files[i].Name(), "-"), strings.Count(files[j].Name(), "-")
		if (a == 0) != (b == 0) {
			return a == 0
		}
		return files[i].Name() > files[j].Name()
	})
	ips := map[string]*trafficreport.IPCount{}
	currentFiles := 0
	hosts, statuses, days := map[string]uint64{}, map[string]uint64{}, map[string]uint64{}
	remaining := maxReadBytes
	for _, file := range files {
		if !logName.MatchString(file.Name()) || !file.Type().IsRegular() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, trafficreport.ErrUnavailable
		}
		if remaining <= 0 || result.FilesRead >= 40 {
			result.Truncated = true
			break
		}
		f, err := root.Open(file.Name())
		if os.IsNotExist(err) {
			result.Truncated = true
			continue
		} // Concurrent rotation.
		if err != nil {
			return result, trafficreport.ErrUnavailable
		}
		var input io.Reader = f
		var compressed *gzip.Reader
		if strings.HasSuffix(file.Name(), ".gz") {
			compressed, err = gzip.NewReader(f)
			if err != nil {
				f.Close()
				result.InvalidRecords++
				result.Truncated = true
				continue
			}
			input = compressed
		}
		limited := &io.LimitedReader{R: input, N: remaining}
		scanner := bufio.NewScanner(limited)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		result.FilesRead++
		if !strings.Contains(file.Name(), "-") && !strings.HasSuffix(file.Name(), ".gz") {
			currentFiles++
		}
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				f.Close()
				return result, trafficreport.ErrUnavailable
			}
			var row record
			if json.Unmarshal(scanner.Bytes(), &row) != nil {
				result.InvalidRecords++
				continue
			}
			address, err := netip.ParseAddr(row.Request.RemoteIP)
			if err != nil || !r.hosts[row.Request.Host] || row.Timestamp <= 0 || row.Timestamp > float64(time.Now().Add(time.Minute).Unix()) || row.Status < 100 || row.Status > 599 || row.Duration < 0 || math.IsInf(row.Duration, 0) || math.IsNaN(row.Duration) {
				result.InvalidRecords++
				continue
			}
			stamp := time.Unix(0, int64(row.Timestamp*1e9)).UTC()
			if result.AvailableFrom == nil || stamp.Before(*result.AvailableFrom) {
				value := stamp
				result.AvailableFrom = &value
			}
			if result.AvailableTo == nil || stamp.After(*result.AvailableTo) {
				value := stamp
				result.AvailableTo = &value
			}
			if stamp.Before(query.From) || !stamp.Before(query.To) {
				continue
			}
			ip := address.Unmap().String()
			item := ips[ip]
			if item == nil {
				if len(ips) >= maxUniqueIPs {
					result.Truncated = true
					break
				}
				item = &trafficreport.IPCount{IP: ip, FirstSeen: stamp, LastSeen: stamp}
				ips[ip] = item
			}
			item.Requests++
			if stamp.Before(item.FirstSeen) {
				item.FirstSeen = stamp
			}
			if stamp.After(item.LastSeen) {
				item.LastSeen = stamp
			}
			result.Requests++
			if row.Status >= 500 {
				result.ServerErrors++
			}
			hosts[row.Request.Host]++
			statuses[strconv.Itoa(row.Status)]++
			days[stamp.Format("2006-01-02")]++
			method := row.Request.Method
			switch method {
			case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "CONNECT", "TRACE":
			default:
				method = "OTHER"
			}
			result.Logs = append(result.Logs, trafficreport.Entry{Time: stamp, IP: ip, Host: row.Request.Host, Method: method, Status: row.Status, DurationMS: math.Round(row.Duration*100000) / 100})
			if len(result.Logs) >= 400 {
				trimLogs(&result)
			}
		}
		if scanner.Err() != nil {
			result.InvalidRecords++
			result.Truncated = true
		}
		remaining = limited.N
		if remaining == 0 {
			result.Truncated = true
		}
		if compressed != nil {
			compressed.Close()
		}
		f.Close()
		if result.Truncated && len(ips) >= maxUniqueIPs {
			break
		}
	}
	if result.FilesRead == 0 {
		return result, trafficreport.ErrUnavailable
	}
	if currentFiles < len(r.hosts) {
		result.Truncated = true
	}
	result.UniqueIPs = len(ips)
	for _, value := range ips {
		result.IPs = append(result.IPs, *value)
	}
	sort.Slice(result.IPs, func(i, j int) bool {
		if result.IPs[i].Requests == result.IPs[j].Requests {
			return result.IPs[i].IP < result.IPs[j].IP
		}
		return result.IPs[i].Requests > result.IPs[j].Requests
	})
	if len(result.IPs) > 1000 {
		result.IPs = result.IPs[:1000]
		result.IPListTruncated = true
	}
	result.Hosts = counts(hosts)
	result.Statuses = counts(statuses)
	result.Days = counts(days)
	trimLogs(&result)
	return result, nil
}

func trimLogs(report *trafficreport.Report) {
	sort.Slice(report.Logs, func(i, j int) bool { return report.Logs[i].Time.After(report.Logs[j].Time) })
	if len(report.Logs) > 200 {
		report.Logs = report.Logs[:200]
	}
}

func counts(values map[string]uint64) []trafficreport.Count {
	result := make([]trafficreport.Count, 0, len(values))
	for label, count := range values {
		result = append(result, trafficreport.Count{Label: label, Requests: count})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Label < result[j].Label })
	return result
}
