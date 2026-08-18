// Package runnerbroker owns the trust and payload boundary between ephemeral
// runner Pods and customer invocation data. Kubernetes authentication is an
// adapter concern; this package receives only a verified invocation identity.
package runnerbroker

import (
	"context"
	"errors"
)

var ErrIdentityDenied = errors.New("runner workload identity was denied")

type Identity struct {
	InvocationID string
	Profile      string
	JobName      string
	JobUID       string
	PodName      string
	PodUID       string
}

type IdentityVerifier interface {
	Verify(context.Context, string, string) (Identity, error)
}
