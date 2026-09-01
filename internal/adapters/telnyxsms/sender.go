// Package telnyxsms delivers Spyglass security codes through the Telnyx
// Messaging API v2. Spyglass remains authoritative for OTP generation and
// verification; Telnyx is used only as the transport.
package telnyxsms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/multifactor"
)

const endpoint = "https://api.telnyx.com/v2/messages"

var e164Pattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

type Config struct {
	APIKey             string
	From               string
	Client             *http.Client
	DevelopmentDiscard bool
}

type Sender struct {
	config   Config
	endpoint string
}

func New(config Config) (*Sender, error) {
	return newWithEndpoint(config, endpoint)
}

func newWithEndpoint(config Config, requestEndpoint string) (*Sender, error) {
	if config.DevelopmentDiscard {
		return &Sender{config: config, endpoint: requestEndpoint}, nil
	}
	if config.APIKey == "" || !e164Pattern.MatchString(config.From) {
		return nil, errors.New("Telnyx SMS requires an API key and E.164 sender")
	}
	if config.Client == nil {
		config.Client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Sender{config: config, endpoint: requestEndpoint}, nil
}

func (s *Sender) SendMultifactor(ctx context.Context, message multifactor.Message) error {
	if message.Kind != multifactor.KindSMS {
		return errors.New("Telnyx SMS sender received a non-SMS message")
	}
	if s.config.DevelopmentDiscard {
		return nil
	}
	payload := struct {
		To   string `json:"to"`
		From string `json:"from"`
		Text string `json:"text"`
	}{
		To:   message.Destination,
		From: s.config.From,
		Text: "Infinite Ocean: security code " + message.Code + ". Expires in 10 min. Msg frequency varies. Msg & data rates may apply. Reply STOP to opt out or HELP for help.",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+s.config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.config.Client.Do(request)
	if err != nil {
		return errors.New("Telnyx SMS is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return errors.New("Telnyx rejected the SMS message")
	}
	var accepted struct {
		Data struct {
			ID string `json:"id"`
			To []struct {
				Status string `json:"status"`
			} `json:"to"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&accepted); err != nil || accepted.Data.ID == "" || len(accepted.Data.To) != 1 || accepted.Data.To[0].Status == "" {
		return errors.New("Telnyx returned an invalid SMS acceptance response")
	}
	return nil
}

var _ multifactor.Sender = (*Sender)(nil)
