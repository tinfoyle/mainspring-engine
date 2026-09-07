package modelgateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

const MaximumPricingBytes = 64 << 10

// ParsePricingJSON decodes the operator-owned model price book used by the
// trusted gateway. Models must be explicit: an unpriced model is unavailable
// and can never silently bypass a Persona cost ceiling.
func ParsePricingJSON(raw string) ([]ModelPrice, error) {
	if strings.TrimSpace(raw) == "" || len(raw) > MaximumPricingBytes {
		return nil, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	var encoded map[string]struct {
		Input       int64  `json:"input_micros_per_million_tokens"`
		CachedInput *int64 `json:"cached_input_micros_per_million_tokens,omitempty"`
		Output      int64  `json:"output_micros_per_million_tokens"`
	}
	if err := decoder.Decode(&encoded); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || len(encoded) == 0 || len(encoded) > 256 {
		return nil, ErrInvalidRequest
	}
	models := make([]string, 0, len(encoded))
	for model := range encoded {
		models = append(models, model)
	}
	sort.Strings(models)
	result := make([]ModelPrice, 0, len(models))
	for _, model := range models {
		price := encoded[model]
		if !validModel.MatchString(model) || price.Input < 0 || price.Input > MaximumPriceMicros || price.Output < 0 || price.Output > MaximumPriceMicros || (price.CachedInput != nil && (*price.CachedInput < 0 || *price.CachedInput > MaximumPriceMicros)) {
			return nil, ErrInvalidRequest
		}
		result = append(result, ModelPrice{Model: model, InputMicrosPerMillionTokens: price.Input, OutputMicrosPerMillionTokens: price.Output, CachedInputMicrosPerMillionTokens: price.CachedInput})
	}
	return result, nil
}
