// Package smswebhook delivers security codes to an operator-selected HTTPS SMS gateway.
package smswebhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/multifactor"
)

type Config struct {
	URL, BearerToken, From string
	Client                 *http.Client
	DevelopmentDiscard     bool
}

type Sender struct{ config Config }

func New(config Config) (*Sender, error) {
	if config.DevelopmentDiscard {
		return &Sender{config: config}, nil
	}
	parsed, err := url.Parse(config.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || config.BearerToken == "" || config.From == "" {
		return nil, errors.New("SMS gateway requires an exact HTTPS URL, bearer token, and sender")
	}
	if config.Client == nil {
		config.Client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Sender{config: config}, nil
}

func (s *Sender) SendMultifactor(ctx context.Context, message multifactor.Message) error {
	if message.Kind != multifactor.KindSMS {
		return errors.New("SMS sender received a non-SMS message")
	}
	if s.config.DevelopmentDiscard {
		return nil
	}
	body, err := json.Marshal(map[string]string{"to": message.Destination, "from": s.config.From, "message": "Infinite Ocean security code: " + message.Code + ". It expires in 10 minutes.", "idempotency_key": message.ID})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.config.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+s.config.BearerToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", message.ID)
	response, err := s.config.Client.Do(request)
	if err != nil {
		return errors.New("SMS gateway is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("SMS gateway rejected the message")
	}
	return nil
}

var _ multifactor.Sender = (*Sender)(nil)
