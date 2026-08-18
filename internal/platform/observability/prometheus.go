// Package observability renders content-free operational metrics. Business
// identifiers and customer content are deliberately not accepted as labels.
package observability

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

var metricLabel = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type autoscalingMetric struct {
	Field, Name, Help string
}

var autoscalingMetrics = map[string]autoscalingMetric{
	"agent-dispatch":    {Field: "ready", Name: "spyglass_agent_dispatch_ready", Help: "Agent dispatch records currently ready to be claimed."},
	"agent-projection":  {Field: "ready", Name: "spyglass_agent_projection_ready", Help: "Agent result projections currently ready to be claimed."},
	"runner-controller": {Field: "ready", Name: "spyglass_runner_ready", Help: "Runner invocations currently ready to be launched."},
}

func RenderWorkerMetrics(worker string, status any) ([]byte, error) {
	if !metricLabel.MatchString(worker) || status == nil {
		return nil, errors.New("worker metric identity and status are required")
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return nil, errors.New("encode worker metric status")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	values := map[string]json.Number{}
	if err := decoder.Decode(&values); err != nil {
		return nil, errors.New("worker metric status must contain only numeric fields")
	}
	fields := make([]string, 0, len(values))
	for field, value := range values {
		if !metricLabel.MatchString(field) {
			return nil, fmt.Errorf("worker metric field %q is invalid", field)
		}
		if _, err := value.Float64(); err != nil {
			return nil, fmt.Errorf("worker metric field %q is not numeric", field)
		}
		fields = append(fields, field)
	}
	sort.Strings(fields)
	var output bytes.Buffer
	output.WriteString("# HELP spyglass_worker_status Current content-free worker status value.\n")
	output.WriteString("# TYPE spyglass_worker_status gauge\n")
	for _, field := range fields {
		fmt.Fprintf(&output, "spyglass_worker_status{worker=%q,field=%q} %s\n", worker, field, values[field].String())
	}
	if metric, ok := autoscalingMetrics[worker]; ok {
		value, exists := values[metric.Field]
		if !exists {
			return nil, fmt.Errorf("worker status has no autoscaling field %q", metric.Field)
		}
		fmt.Fprintf(&output, "# HELP %s %s\n# TYPE %s gauge\n%s %s\n", metric.Name, metric.Help, metric.Name, metric.Name, value.String())
	}
	return output.Bytes(), nil
}
