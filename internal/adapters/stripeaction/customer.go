// Package stripeaction implements approved Stripe effects for the runner
// capability boundary. Provider credentials never enter a runner pod.
package stripeaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/stripe"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
)

const (
	CustomerCreateCapability = "stripe.customer.create"
	maximumResponseBytes     = 1 << 20
)

type CustomerHandler struct {
	secretKey, apiVersion, baseURL string
	http                           *http.Client
}

func NewCustomerHandler(secretKey, apiVersion string, httpClient *http.Client) (*CustomerHandler, error) {
	if (!strings.HasPrefix(secretKey, "sk_test_") && !strings.HasPrefix(secretKey, "sk_live_")) || len(secretKey) < 12 {
		return nil, errors.New("Stripe action secret key is invalid")
	}
	if apiVersion == "" {
		apiVersion = stripe.DefaultAPIVersion
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &CustomerHandler{secretKey: secretKey, apiVersion: apiVersion, baseURL: "https://api.stripe.com", http: httpClient}, nil
}

type customerInput struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

type customerResponse struct {
	ID       string `json:"id"`
	Metadata struct {
		AccountID   string `json:"spyglass_account_id"`
		OperationID string `json:"spyglass_operation_id"`
	} `json:"metadata"`
}

func (h *CustomerHandler) Execute(ctx context.Context, call runnercapability.AuthorizedCall) (json.RawMessage, error) {
	input, err := decodeInput(call.Input)
	if err != nil || call.Action == nil || call.Action.IdempotencyKey != call.OperationID {
		return nil, actionError{code: "stripe_input_rejected", definitive: true}
	}
	values := url.Values{
		"email":                                  {input.Email},
		"name":                                   {input.Name},
		"metadata[spyglass_account_id]":          {string(call.Grant.AccountID)},
		"metadata[spyglass_operation_id]":        {call.OperationID},
		"metadata[spyglass_executor_capability]": {CustomerCreateCapability},
	}
	var customer customerResponse
	if err := h.request(ctx, http.MethodPost, "/v1/customers", values, call.Action.IdempotencyKey, &customer); err != nil {
		return nil, err
	}
	if !boundCustomer(customer, string(call.Grant.AccountID), call.OperationID) {
		return nil, actionError{code: "stripe_response_invalid"}
	}
	return customerOutput(customer.ID), nil
}

func (h *CustomerHandler) Reconcile(ctx context.Context, call runnercapability.AuthorizedCall) (json.RawMessage, runnercapability.ActionOutcome, error) {
	if call.Action == nil || call.Action.IdempotencyKey != call.OperationID {
		return nil, runnercapability.ActionUnknown, actionError{code: "stripe_binding_invalid"}
	}
	query := fmt.Sprintf("metadata['spyglass_operation_id']:'%s' AND metadata['spyglass_account_id']:'%s'", call.OperationID, call.Grant.AccountID)
	values := url.Values{"query": {query}, "limit": {"2"}}
	var result struct {
		Data []customerResponse `json:"data"`
	}
	if err := h.request(ctx, http.MethodGet, "/v1/customers/search?"+values.Encode(), nil, "", &result); err != nil {
		return nil, runnercapability.ActionUnknown, err
	}
	if len(result.Data) == 0 {
		return nil, runnercapability.ActionUnknown, actionError{code: "stripe_not_visible"}
	}
	if len(result.Data) != 1 || !boundCustomer(result.Data[0], string(call.Grant.AccountID), call.OperationID) {
		return nil, runnercapability.ActionUnknown, actionError{code: "stripe_lookup_conflict"}
	}
	return customerOutput(result.Data[0].ID), runnercapability.ActionSucceeded, nil
}

func decodeInput(raw json.RawMessage) (customerInput, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input customerInput
	if err := decoder.Decode(&input); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return customerInput{}, errors.New("invalid input")
	}
	input.Email, input.Name = strings.TrimSpace(input.Email), strings.TrimSpace(input.Name)
	address, err := mail.ParseAddress(input.Email)
	if err != nil || address.Address != input.Email || len(input.Email) > 254 || len(input.Name) < 1 || len(input.Name) > 200 || strings.ContainsAny(input.Name, "\r\n") {
		return customerInput{}, errors.New("invalid customer")
	}
	return input, nil
}

func boundCustomer(customer customerResponse, accountID, operationID string) bool {
	return strings.HasPrefix(customer.ID, "cus_") && len(customer.ID) <= 255 && customer.Metadata.AccountID == accountID && customer.Metadata.OperationID == operationID
}

func customerOutput(customerID string) json.RawMessage {
	encoded, _ := json.Marshal(struct {
		CustomerID string `json:"customer_id"`
	}{CustomerID: customerID})
	return encoded
}

func (h *CustomerHandler) request(ctx context.Context, method, path string, values url.Values, idempotencyKey string, target any) error {
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, body)
	if err != nil {
		return actionError{code: "stripe_request_invalid", definitive: true}
	}
	request.SetBasicAuth(h.secretKey, "")
	request.Header.Set("Stripe-Version", h.apiVersion)
	if values != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := h.http.Do(request)
	if err != nil {
		return actionError{code: "stripe_transport_unknown"}
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maximumResponseBytes)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, limited)
		switch {
		case response.StatusCode == http.StatusTooManyRequests:
			return actionError{code: "stripe_rate_limited", definitive: true}
		case response.StatusCode >= 400 && response.StatusCode < 500:
			return actionError{code: "stripe_request_rejected", definitive: true}
		default:
			return actionError{code: "stripe_provider_unknown"}
		}
	}
	if err := json.NewDecoder(limited).Decode(target); err != nil {
		return actionError{code: "stripe_response_invalid"}
	}
	return nil
}

type actionError struct {
	code       string
	definitive bool
}

func (e actionError) Error() string    { return e.code }
func (e actionError) Code() string     { return e.code }
func (e actionError) Definitive() bool { return e.definitive }

var _ runnercapability.ConsequentialHandler = (*CustomerHandler)(nil)
