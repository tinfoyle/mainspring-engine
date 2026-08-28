package operationsstaff

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunRejectsUnsafeGovernanceInputBeforeConnecting(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		contains  string
	}{
		{name: "unknown action", arguments: []string{"delete", "--email=staff@example.com"}, contains: "assign, revoke, or show"},
		{name: "non exact email", arguments: []string{"show", "--email=Staff @example.com"}, contains: "exact normalized"},
		{name: "missing audit", arguments: []string{"assign", "--email=staff@example.com", "--role=support"}, contains: "valid role, actor"},
		{name: "unknown role", arguments: []string{"assign", "--email=staff@example.com", "--role=superuser", "--actor=owner", "--reason=Approved support access"}, contains: "valid role, actor"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Run(context.Background(), Config{DatabaseURL: "postgres://invalid", Environment: "local", Arguments: test.arguments, Output: &bytes.Buffer{}})
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
