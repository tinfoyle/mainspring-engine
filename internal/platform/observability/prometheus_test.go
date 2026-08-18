package observability_test

import (
	"bytes"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/platform/observability"
)

func TestWorkerMetricsExposeContentFreeStatusAndAutoscalingGauge(t *testing.T) {
	status := struct {
		Ready      uint64 `json:"ready"`
		DeadLetter uint64 `json:"dead_letter"`
	}{Ready: 7, DeadLetter: 2}
	output, err := observability.RenderWorkerMetrics("agent-dispatch", status)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range [][]byte{
		[]byte(`spyglass_worker_status{worker="agent-dispatch",field="dead_letter"} 2`),
		[]byte(`spyglass_worker_status{worker="agent-dispatch",field="ready"} 7`),
		[]byte("spyglass_agent_dispatch_ready 7"),
	} {
		if !bytes.Contains(output, expected) {
			t.Fatalf("metrics missing %s:\n%s", expected, output)
		}
	}
}

func TestWorkerMetricsRejectLabelsAndNonNumericStatus(t *testing.T) {
	if _, err := observability.RenderWorkerMetrics("account/customer", map[string]uint64{"ready": 1}); err == nil {
		t.Fatal("customer-derived worker label was accepted")
	}
	if _, err := observability.RenderWorkerMetrics("agent-dispatch", map[string]string{"ready": "many"}); err == nil {
		t.Fatal("non-numeric status was accepted")
	}
	if _, err := observability.RenderWorkerMetrics("runner-controller", map[string]uint64{"launched": 1}); err == nil {
		t.Fatal("missing autoscaling gauge was accepted")
	}
}
