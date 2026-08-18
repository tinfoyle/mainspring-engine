// Package openapifixture validates real HTTP responses against the committed
// Spyglass OpenAPI subset. It is test support, not a production request path.
package openapifixture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Contract struct {
	root  map[string]any
	paths map[string]any
}

func Load(path string) (*Contract, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	paths, ok := object(root["paths"])
	if !ok {
		return nil, errors.New("OpenAPI paths object is required")
	}
	return &Contract{root: root, paths: paths}, nil
}

// ValidateResponse checks status selection, required headers, media type, and
// the resolved JSON Schema of one actual handler response.
func (c *Contract) ValidateResponse(method, target string, status int, headers http.Header, body []byte) error {
	parsed, err := url.Parse(target)
	if err != nil {
		return err
	}
	pathTemplate, item := c.matchPath(parsed.Path)
	if item == nil {
		return fmt.Errorf("%s %s is not in OpenAPI", method, parsed.Path)
	}
	operation, ok := object(item[strings.ToLower(method)])
	if !ok {
		return fmt.Errorf("%s %s is not in OpenAPI", method, pathTemplate)
	}
	responses, ok := object(operation["responses"])
	if !ok {
		return fmt.Errorf("%s %s has no responses", method, pathTemplate)
	}
	responseRaw, exists := responses[strconv.Itoa(status)]
	if !exists {
		responseRaw, exists = responses["default"]
	}
	if !exists {
		return fmt.Errorf("%s %s does not document status %d", method, pathTemplate, status)
	}
	response, ok := object(c.resolve(responseRaw))
	if !ok {
		return errors.New("documented response is invalid")
	}
	if err := c.validateHeaders(response, headers); err != nil {
		return fmt.Errorf("%s %s status %d: %w", method, pathTemplate, status, err)
	}
	content, _ := object(response["content"])
	if len(content) == 0 {
		if len(bytes.TrimSpace(body)) != 0 {
			return fmt.Errorf("%s %s status %d returned an undocumented body", method, pathTemplate, status)
		}
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(headers.Get("Content-Type"))
	if err != nil {
		return fmt.Errorf("%s %s status %d has invalid Content-Type: %w", method, pathTemplate, status, err)
	}
	media, ok := object(content[mediaType])
	if !ok {
		return fmt.Errorf("%s %s status %d returned undocumented media type %q", method, pathTemplate, status, mediaType)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode response JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("response body contains more than one JSON value")
	}
	if err := c.validateSchema(media["schema"], value, "$", 0); err != nil {
		return fmt.Errorf("%s %s status %d: %w", method, pathTemplate, status, err)
	}
	return nil
}

func (c *Contract) matchPath(path string) (string, map[string]any) {
	if exact, ok := object(c.paths[path]); ok {
		return path, exact
	}
	for template, raw := range c.paths {
		if pathMatches(template, path) {
			item, _ := object(raw)
			return template, item
		}
	}
	return "", nil
}

func pathMatches(template, actual string) bool {
	want := strings.Split(strings.Trim(template, "/"), "/")
	got := strings.Split(strings.Trim(actual, "/"), "/")
	if len(want) != len(got) {
		return false
	}
	for index := range want {
		if strings.HasPrefix(want[index], "{") && strings.HasSuffix(want[index], "}") {
			if got[index] == "" {
				return false
			}
			continue
		}
		if want[index] != got[index] {
			return false
		}
	}
	return true
}

func (c *Contract) validateHeaders(response map[string]any, actual http.Header) error {
	headers, _ := object(response["headers"])
	for name, raw := range headers {
		definition, ok := object(c.resolve(raw))
		if !ok {
			return fmt.Errorf("header %s definition is invalid", name)
		}
		values := actual.Values(name)
		if boolean(definition["required"]) && len(values) == 0 {
			return fmt.Errorf("required header %s is missing", name)
		}
		for index, value := range values {
			if err := c.validateSchema(definition["schema"], value, "$header."+name+"["+strconv.Itoa(index)+"]", 0); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Contract) validateSchema(raw, value any, path string, depth int) error {
	if depth > 64 {
		return fmt.Errorf("%s exceeded schema depth", path)
	}
	schema, ok := object(c.resolve(raw))
	if !ok {
		return fmt.Errorf("%s has an invalid schema", path)
	}
	if alternatives, ok := schema["anyOf"].([]any); ok {
		for _, alternative := range alternatives {
			if c.validateSchema(alternative, value, path, depth+1) == nil {
				return nil
			}
		}
		return fmt.Errorf("%s does not match any allowed schema", path)
	}
	if values, ok := schema["enum"].([]any); ok && !containsJSON(values, value) {
		return fmt.Errorf("%s is not an allowed enum value", path)
	}
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "object":
		objectValue, ok := object(value)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		properties, _ := object(schema["properties"])
		for required := range stringSet(schema["required"]) {
			if _, exists := objectValue[required]; !exists {
				return fmt.Errorf("%s.%s is required", path, required)
			}
		}
		for name, candidate := range objectValue {
			property, known := properties[name]
			if known {
				if err := c.validateSchema(property, candidate, path+"."+name, depth+1); err != nil {
					return err
				}
				continue
			}
			switch additional := schema["additionalProperties"].(type) {
			case bool:
				if !additional {
					return fmt.Errorf("%s.%s is not allowed", path, name)
				}
			case map[string]any:
				if err := c.validateSchema(additional, candidate, path+"."+name, depth+1); err != nil {
					return err
				}
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		if err := validateLength(schema, int64(len(items)), path, "Items"); err != nil {
			return err
		}
		if boolean(schema["uniqueItems"]) && !uniqueJSON(items) {
			return fmt.Errorf("%s must contain unique items", path)
		}
		for index, item := range items {
			if err := c.validateSchema(schema["items"], item, path+"["+strconv.Itoa(index)+"]", depth+1); err != nil {
				return err
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s must be a string", path)
		}
		if err := validateLength(schema, int64(utf8.RuneCountInString(text)), path, "Length"); err != nil {
			return err
		}
		if pattern, ok := schema["pattern"].(string); ok {
			compiled, err := regexp.Compile(pattern)
			if err != nil || !compiled.MatchString(text) {
				return fmt.Errorf("%s does not match %q", path, pattern)
			}
		}
		if err := validateFormat(schema["format"], text); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("%s must be an integer", path)
		}
		integer, err := number.Int64()
		if err != nil {
			return fmt.Errorf("%s must be an integer", path)
		}
		if err := validateNumber(schema, float64(integer), path); err != nil {
			return err
		}
	case "number":
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("%s must be a number", path)
		}
		parsed, err := number.Float64()
		if err != nil {
			return fmt.Errorf("%s must be a number", path)
		}
		if err := validateNumber(schema, parsed, path); err != nil {
			return err
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
	case "null":
		if value != nil {
			return fmt.Errorf("%s must be null", path)
		}
	default:
		return fmt.Errorf("%s uses unsupported schema type %q", path, typeName)
	}
	return nil
}

func (c *Contract) resolve(raw any) any {
	current := raw
	for depth := 0; depth < 64; depth++ {
		value, ok := object(current)
		if !ok {
			return current
		}
		reference, ok := value["$ref"].(string)
		if !ok {
			return current
		}
		if !strings.HasPrefix(reference, "#/") {
			return nil
		}
		current = c.root
		for _, part := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
			container, ok := object(current)
			if !ok {
				return nil
			}
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			current = container[part]
		}
	}
	return nil
}

func validateLength(schema map[string]any, value int64, path, suffix string) error {
	if minimum, ok := integer(schema["min"+suffix]); ok && value < minimum {
		return fmt.Errorf("%s is shorter than %d", path, minimum)
	}
	if maximum, ok := integer(schema["max"+suffix]); ok && value > maximum {
		return fmt.Errorf("%s is longer than %d", path, maximum)
	}
	return nil
}

func validateNumber(schema map[string]any, value float64, path string) error {
	if minimum, ok := decimal(schema["minimum"]); ok && value < minimum {
		return fmt.Errorf("%s is less than %v", path, minimum)
	}
	if maximum, ok := decimal(schema["maximum"]); ok && value > maximum {
		return fmt.Errorf("%s exceeds %v", path, maximum)
	}
	return nil
}

func validateFormat(raw any, value string) error {
	format, _ := raw.(string)
	switch format {
	case "", "password":
		return nil
	case "uuid":
		if err := ids.Validate(value); err != nil {
			return errors.New("must be a UUID")
		}
	case "date-time":
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return errors.New("must be an RFC 3339 date-time")
		}
	case "email":
		parsed, err := mail.ParseAddress(value)
		if err != nil || parsed.Address != value {
			return errors.New("must be an email address")
		}
	case "uri":
		parsed, err := url.Parse(value)
		if err != nil || !parsed.IsAbs() {
			return errors.New("must be an absolute URI")
		}
	default:
		return fmt.Errorf("uses unsupported format %q", format)
	}
	return nil
}

func object(raw any) (map[string]any, bool) {
	value, ok := raw.(map[string]any)
	return value, ok
}

func boolean(raw any) bool {
	value, _ := raw.(bool)
	return value
}

func integer(raw any) (int64, bool) {
	value, ok := raw.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := value.Int64()
	return parsed, err == nil
}

func decimal(raw any) (float64, bool) {
	value, ok := raw.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := value.Float64()
	return parsed, err == nil
}

func stringSet(raw any) map[string]bool {
	result := map[string]bool{}
	values, _ := raw.([]any)
	for _, value := range values {
		if text, ok := value.(string); ok {
			result[text] = true
		}
	}
	return result
}

func containsJSON(values []any, candidate any) bool {
	want, _ := json.Marshal(candidate)
	for _, value := range values {
		raw, _ := json.Marshal(value)
		if bytes.Equal(raw, want) {
			return true
		}
	}
	return false
}

func uniqueJSON(values []any) bool {
	seen := map[string]bool{}
	for _, value := range values {
		raw, _ := json.Marshal(value)
		if seen[string(raw)] {
			return false
		}
		seen[string(raw)] = true
	}
	return true
}
