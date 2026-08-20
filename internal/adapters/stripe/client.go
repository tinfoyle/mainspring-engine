// Package stripe implements the billing.Provider boundary using Stripe's v1
// HTTP API. Provider JSON is translated here and never leaks into the domain.
package stripe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const DefaultAPIVersion = "2026-07-29.dahlia"

type Client struct {
	secretKey  string
	apiVersion string
	baseURL    string
	http       *http.Client
	mode       string
}

func New(secretKey, apiVersion string, httpClient *http.Client) (*Client, error) {
	mode := ""
	if strings.HasPrefix(secretKey, "sk_test_") {
		mode = "test"
	}
	if strings.HasPrefix(secretKey, "sk_live_") {
		mode = "live"
	}
	if mode == "" || len(secretKey) < 12 {
		return nil, errors.New("Stripe secret key must be a non-placeholder sk_ value")
	}
	if apiVersion == "" {
		apiVersion = DefaultAPIVersion
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{secretKey: secretKey, apiVersion: apiVersion, baseURL: "https://api.stripe.com", http: httpClient, mode: mode}, nil
}

func (c *Client) Mode() string { return c.mode }

func (c *Client) CreateCustomer(ctx context.Context, command billing.CreateCustomerCommand) (billing.CustomerReference, error) {
	values := url.Values{
		"email":                         {command.Email},
		"name":                          {command.Name},
		"metadata[spyglass_account_id]": {string(command.AccountID)},
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := c.request(ctx, http.MethodPost, "/v1/customers", values, command.IdempotencyKey, &response); err != nil {
		return billing.CustomerReference{}, err
	}
	if !strings.HasPrefix(response.ID, "cus_") {
		return billing.CustomerReference{}, errors.New("Stripe returned an invalid customer")
	}
	return billing.CustomerReference{ID: response.ID}, nil
}

func (c *Client) CreateCheckoutSession(ctx context.Context, command billing.CreateCheckoutCommand) (billing.HostedSession, error) {
	values := url.Values{
		"mode":                             {"subscription"},
		"customer":                         {command.CustomerID},
		"client_reference_id":              {string(command.AccountID)},
		"line_items[0][price]":             {command.StripePriceID},
		"line_items[0][quantity]":          {"1"},
		"success_url":                      {command.SuccessURL},
		"cancel_url":                       {command.CancelURL},
		"metadata[spyglass_account_id]":    {string(command.AccountID)},
		"metadata[spyglass_offer_code]":    {command.OfferCode},
		"metadata[spyglass_offer_version]": {strconv.FormatUint(command.OfferVersion, 10)},
		"subscription_data[metadata][spyglass_account_id]":    {string(command.AccountID)},
		"subscription_data[metadata][spyglass_offer_code]":    {command.OfferCode},
		"subscription_data[metadata][spyglass_offer_version]": {strconv.FormatUint(command.OfferVersion, 10)},
	}
	var response hostedResponse
	if err := c.request(ctx, http.MethodPost, "/v1/checkout/sessions", values, command.IdempotencyKey, &response); err != nil {
		return billing.HostedSession{}, err
	}
	return response.session("cs_", "checkout")
}

func (c *Client) CreatePortalSession(ctx context.Context, command billing.CreatePortalCommand) (billing.HostedSession, error) {
	values := url.Values{"customer": {command.CustomerID}, "return_url": {command.ReturnURL}}
	var response hostedResponse
	if err := c.request(ctx, http.MethodPost, "/v1/billing_portal/sessions", values, command.IdempotencyKey, &response); err != nil {
		return billing.HostedSession{}, err
	}
	return response.session("bps_", "portal")
}

type hostedResponse struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expires_at"`
}

func (r hostedResponse) session(prefix, kind string) (billing.HostedSession, error) {
	parsed, err := url.Parse(r.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || !strings.HasPrefix(r.ID, prefix) {
		return billing.HostedSession{}, fmt.Errorf("Stripe returned an invalid %s session", kind)
	}
	return billing.HostedSession{ID: r.ID, URL: r.URL, ExpiresAt: unixTime(r.ExpiresAt)}, nil
}

func (c *Client) RetrieveSubscription(ctx context.Context, subscriptionID string) (billing.ProviderSubscription, error) {
	if !strings.HasPrefix(subscriptionID, "sub_") {
		return billing.ProviderSubscription{}, errors.New("invalid Stripe subscription ID")
	}
	var response subscriptionResponse
	path := "/v1/subscriptions/" + url.PathEscape(subscriptionID)
	if err := c.request(ctx, http.MethodGet, path, nil, "", &response); err != nil {
		return billing.ProviderSubscription{}, err
	}
	result := billing.ProviderSubscription{ID: response.ID, Mode: c.mode, CustomerID: response.Customer, State: response.Status, CancelAt: unixPointer(response.CancelAt), CollectionPaused: response.PauseCollection != nil, AccountID: ids.AccountID(response.Metadata.AccountID), OfferCode: response.Metadata.OfferCode}
	result.OfferVersion, _ = strconv.ParseUint(response.Metadata.OfferVersion, 10, 64)
	for _, item := range response.Items.Data {
		if item.Price.ID != "" {
			result.PriceIDs = append(result.PriceIDs, item.Price.ID)
		}
		if result.CurrentPeriodStart.IsZero() || item.CurrentPeriodStart < result.CurrentPeriodStart.Unix() {
			result.CurrentPeriodStart = unixTime(item.CurrentPeriodStart)
		}
		if item.CurrentPeriodEnd > result.CurrentPeriodEnd.Unix() {
			result.CurrentPeriodEnd = unixTime(item.CurrentPeriodEnd)
		}
	}
	if result.CurrentPeriodStart.IsZero() {
		result.CurrentPeriodStart = unixTime(response.CurrentPeriodStart)
		result.CurrentPeriodEnd = unixTime(response.CurrentPeriodEnd)
	}
	if !strings.HasPrefix(result.ID, "sub_") || !strings.HasPrefix(result.CustomerID, "cus_") {
		return billing.ProviderSubscription{}, errors.New("Stripe returned an invalid subscription")
	}
	versionMaterial, _ := json.Marshal(response)
	result.ObjectVersion = fmt.Sprintf("%x", sha256.Sum256(versionMaterial))
	return result, nil
}

type subscriptionResponse struct {
	ID                 string `json:"id"`
	Customer           string `json:"customer"`
	Status             string `json:"status"`
	Created            int64  `json:"created"`
	CurrentPeriodStart int64  `json:"current_period_start"`
	CurrentPeriodEnd   int64  `json:"current_period_end"`
	CancelAt           int64  `json:"cancel_at"`
	PauseCollection    any    `json:"pause_collection"`
	Metadata           struct {
		AccountID    string `json:"spyglass_account_id"`
		OfferCode    string `json:"spyglass_offer_code"`
		OfferVersion string `json:"spyglass_offer_version"`
	} `json:"metadata"`
	Items struct {
		Data []struct {
			CurrentPeriodStart int64 `json:"current_period_start"`
			CurrentPeriodEnd   int64 `json:"current_period_end"`
			Price              struct {
				ID string `json:"id"`
			} `json:"price"`
		} `json:"data"`
	} `json:"items"`
}

func (c *Client) request(ctx context.Context, method, path string, values url.Values, idempotencyKey string, target any) error {
	var body io.Reader
	if values != nil {
		body = bytes.NewBufferString(values.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	request.SetBasicAuth(c.secretKey, "")
	request.Header.Set("Stripe-Version", c.apiVersion)
	if values != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("Stripe request failed: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 1<<20)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error struct {
				Type string `json:"type"`
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(limited).Decode(&failure)
		return fmt.Errorf("Stripe request failed (%d, %s, %s)", response.StatusCode, failure.Error.Type, failure.Error.Code)
	}
	if err := json.NewDecoder(limited).Decode(target); err != nil {
		return fmt.Errorf("decode Stripe response: %w", err)
	}
	return nil
}

func unixTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}

func unixPointer(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	result := unixTime(value)
	return &result
}

var _ billing.Provider = (*Client)(nil)
