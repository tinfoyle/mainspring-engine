package main

import "testing"

func TestValidateLocalAgentJourneyTargets(t *testing.T) {
	valid := config{
		appOrigin: "https://app.infiniteocean.localhost:8444", mcpOrigin: "https://mcp.infiniteocean.localhost:8444",
		edgeAddress: "edge:443", mailpitURL: "http://mailpit:8025", databaseURL: "postgres://fixture@global-db:5432/spyglass", providerFixture: "deterministic-fail-once",
	}
	if err := validateLocalAgentJourneyTargets(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*config){
		"remote edge":     func(value *config) { value.edgeAddress = "203.0.113.10:443" },
		"remote mail":     func(value *config) { value.mailpitURL = "https://mail.example.test" },
		"remote database": func(value *config) { value.databaseURL = "postgres://fixture@global-db.example.test/spyglass" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if err := validateLocalAgentJourneyTargets(candidate); err == nil {
				t.Fatal("expected non-local target rejection")
			}
		})
	}
}
