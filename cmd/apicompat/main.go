// Command apicompat rejects backward-incompatible customer OpenAPI changes.
// It deliberately covers Spyglass's bounded JSON/header contract subset rather
// than pretending to be a complete OpenAPI semantic-diff implementation.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
)

var httpMethods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}

type contract struct {
	root       map[string]any
	operations map[string]operation
}

type operation struct {
	pathItem map[string]any
	value    map[string]any
}

type direction string

const (
	requestDirection  direction = "request"
	responseDirection direction = "response"
)

func main() {
	basePath := flag.String("base", "", "base OpenAPI document")
	headPath := flag.String("head", "", "candidate OpenAPI document")
	flag.Parse()
	if *basePath == "" || *headPath == "" || flag.NArg() != 0 {
		fatal(errors.New("usage: go run ./cmd/apicompat -base <base.json> -head <head.json>"))
	}
	base, err := load(*basePath)
	if err != nil {
		fatal(fmt.Errorf("load base contract: %w", err))
	}
	head, err := load(*headPath)
	if err != nil {
		fatal(fmt.Errorf("load candidate contract: %w", err))
	}
	breaks := compare(base, head)
	if len(breaks) > 0 {
		for _, value := range breaks {
			fmt.Fprintln(os.Stderr, "BREAKING:", value)
		}
		os.Exit(1)
	}
	fmt.Printf("API compatibility check passed (%d existing operations)\n", len(base.operations))
}

func load(path string) (contract, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return contract{}, err
	}
	var root map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return contract{}, err
	}
	paths, ok := object(root["paths"])
	if !ok {
		return contract{}, errors.New("paths object is required")
	}
	result := contract{root: root, operations: map[string]operation{}}
	for path, rawItem := range paths {
		item, ok := object(rawItem)
		if !ok {
			return contract{}, fmt.Errorf("path %s is not an object", path)
		}
		for method, rawOperation := range item {
			method = strings.ToLower(method)
			if !httpMethods[method] {
				continue
			}
			value, ok := object(rawOperation)
			if !ok {
				return contract{}, fmt.Errorf("operation %s %s is not an object", method, path)
			}
			result.operations[strings.ToUpper(method)+" "+path] = operation{pathItem: item, value: value}
		}
	}
	return result, nil
}

func compare(base, head contract) []string {
	var breaks []string
	keys := make([]string, 0, len(base.operations))
	for key := range base.operations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		oldOperation := base.operations[key]
		newOperation, exists := head.operations[key]
		if !exists {
			breaks = append(breaks, key+" was removed")
			continue
		}
		for _, field := range []string{"operationId", "x-spyglass-service", "security"} {
			if !reflect.DeepEqual(oldOperation.value[field], newOperation.value[field]) {
				breaks = append(breaks, fmt.Sprintf("%s changed %s", key, field))
			}
		}
		breaks = append(breaks, compareParameters(key, base, head, oldOperation, newOperation)...)
		breaks = append(breaks, compareRequestBody(key, base, head, oldOperation.value["requestBody"], newOperation.value["requestBody"])...)
		breaks = append(breaks, compareResponses(key, base, head, oldOperation.value["responses"], newOperation.value["responses"])...)
	}
	return breaks
}

func compareParameters(key string, base, head contract, oldOperation, newOperation operation) []string {
	oldParameters := parameters(base, oldOperation)
	newParameters := parameters(head, newOperation)
	var breaks []string
	for identity, oldParameter := range oldParameters {
		newParameter, exists := newParameters[identity]
		if !exists {
			breaks = append(breaks, fmt.Sprintf("%s removed parameter %s", key, identity))
			continue
		}
		breaks = append(breaks, compareSchema(key+" parameter "+identity, base.root, head.root, oldParameter["schema"], newParameter["schema"], requestDirection, 0)...)
	}
	for identity, newParameter := range newParameters {
		if _, existed := oldParameters[identity]; !existed && boolean(newParameter["required"]) {
			breaks = append(breaks, fmt.Sprintf("%s added required parameter %s", key, identity))
		}
	}
	return breaks
}

func parameters(source contract, op operation) map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, raw := range []any{op.pathItem["parameters"], op.value["parameters"]} {
		values, _ := raw.([]any)
		for _, candidate := range values {
			resolved, ok := object(resolve(source.root, candidate))
			if !ok {
				continue
			}
			name, _ := resolved["name"].(string)
			location, _ := resolved["in"].(string)
			result[location+":"+strings.ToLower(name)] = resolved
		}
	}
	return result
}

func compareRequestBody(key string, base, head contract, oldRaw, newRaw any) []string {
	if oldRaw == nil {
		if body, ok := object(resolve(head.root, newRaw)); ok && boolean(body["required"]) {
			return []string{key + " added a required request body"}
		}
		return nil
	}
	oldBody, oldOK := object(resolve(base.root, oldRaw))
	newBody, newOK := object(resolve(head.root, newRaw))
	if !oldOK || !newOK {
		return []string{key + " removed or invalidated its request body"}
	}
	oldSchema := contentSchema(base.root, oldBody)
	newSchema := contentSchema(head.root, newBody)
	if oldSchema != nil && newSchema == nil {
		return []string{key + " removed application/json request support"}
	}
	return compareSchema(key+" request body", base.root, head.root, oldSchema, newSchema, requestDirection, 0)
}

func compareResponses(key string, base, head contract, oldRaw, newRaw any) []string {
	oldResponses, oldOK := object(oldRaw)
	newResponses, newOK := object(newRaw)
	if !oldOK || !newOK {
		return []string{key + " removed its responses"}
	}
	var breaks []string
	for status, oldRawResponse := range oldResponses {
		if len(status) != 3 || status[0] != '2' {
			continue
		}
		newRawResponse, exists := newResponses[status]
		if !exists {
			breaks = append(breaks, fmt.Sprintf("%s removed success status %s", key, status))
			continue
		}
		oldResponse, _ := object(resolve(base.root, oldRawResponse))
		newResponse, _ := object(resolve(head.root, newRawResponse))
		oldSchema := contentSchema(base.root, oldResponse)
		newSchema := contentSchema(head.root, newResponse)
		if oldSchema != nil && newSchema == nil {
			breaks = append(breaks, fmt.Sprintf("%s status %s removed its JSON response", key, status))
		} else {
			breaks = append(breaks, compareSchema(key+" response "+status, base.root, head.root, oldSchema, newSchema, responseDirection, 0)...)
		}
		breaks = append(breaks, compareResponseHeaders(key+" response "+status, base, head, oldResponse, newResponse)...)
	}
	return breaks
}

func compareResponseHeaders(context string, base, head contract, oldResponse, newResponse map[string]any) []string {
	oldHeaders, _ := object(oldResponse["headers"])
	newHeaders, _ := object(newResponse["headers"])
	var breaks []string
	for name, oldHeader := range oldHeaders {
		newHeader, exists := newHeaders[name]
		if !exists {
			breaks = append(breaks, context+" removed header "+name)
			continue
		}
		oldValue, _ := object(resolve(base.root, oldHeader))
		newValue, _ := object(resolve(head.root, newHeader))
		breaks = append(breaks, compareSchema(context+" header "+name, base.root, head.root, oldValue["schema"], newValue["schema"], responseDirection, 0)...)
	}
	return breaks
}

func contentSchema(root map[string]any, value map[string]any) any {
	content, _ := object(value["content"])
	media, _ := object(content["application/json"])
	if media == nil {
		media, _ = object(content["application/problem+json"])
	}
	return media["schema"]
}

func compareSchema(context string, oldRoot, newRoot map[string]any, oldRaw, newRaw any, mode direction, depth int) []string {
	if oldRaw == nil {
		return nil
	}
	if newRaw == nil {
		return []string{context + " removed its schema"}
	}
	if depth > 64 {
		return []string{context + " exceeded schema comparison depth"}
	}
	oldSchema, oldOK := object(resolve(oldRoot, oldRaw))
	newSchema, newOK := object(resolve(newRoot, newRaw))
	if !oldOK || !newOK {
		return []string{context + " has an invalid schema"}
	}
	oldAny, oldHasAny := oldSchema["anyOf"]
	newAny, newHasAny := newSchema["anyOf"]
	if oldHasAny || newHasAny {
		if !reflect.DeepEqual(expand(oldRoot, oldAny, 0), expand(newRoot, newAny, 0)) {
			return []string{context + " changed an anyOf contract"}
		}
		return nil
	}
	oldType, _ := oldSchema["type"].(string)
	newType, _ := newSchema["type"].(string)
	if oldType != newType {
		return []string{fmt.Sprintf("%s changed type from %q to %q", context, oldType, newType)}
	}
	var breaks []string
	breaks = append(breaks, compareEnum(context, oldSchema["enum"], newSchema["enum"], mode)...)
	for _, field := range []string{"format", "pattern"} {
		if !reflect.DeepEqual(oldSchema[field], newSchema[field]) {
			breaks = append(breaks, context+" changed "+field)
		}
	}
	breaks = append(breaks, compareBounds(context, oldSchema, newSchema, mode)...)
	switch oldType {
	case "array":
		breaks = append(breaks, compareSchema(context+" items", oldRoot, newRoot, oldSchema["items"], newSchema["items"], mode, depth+1)...)
	case "object":
		oldProperties, _ := object(oldSchema["properties"])
		newProperties, _ := object(newSchema["properties"])
		for name, oldProperty := range oldProperties {
			newProperty, exists := newProperties[name]
			if !exists {
				breaks = append(breaks, context+" removed property "+name)
				continue
			}
			breaks = append(breaks, compareSchema(context+" property "+name, oldRoot, newRoot, oldProperty, newProperty, mode, depth+1)...)
		}
		oldRequired := stringSet(oldSchema["required"])
		newRequired := stringSet(newSchema["required"])
		if mode == requestDirection {
			for name := range newRequired {
				if !oldRequired[name] {
					breaks = append(breaks, context+" made property "+name+" required")
				}
			}
		} else {
			for name := range oldRequired {
				if !newRequired[name] {
					breaks = append(breaks, context+" made required response property "+name+" optional")
				}
			}
		}
		breaks = append(breaks, compareAdditionalProperties(context, oldRoot, newRoot, oldSchema["additionalProperties"], newSchema["additionalProperties"], mode, depth+1)...)
	}
	return breaks
}

func compareEnum(context string, oldRaw, newRaw any, mode direction) []string {
	oldValues, oldOK := oldRaw.([]any)
	newValues, newOK := newRaw.([]any)
	if !oldOK && !newOK {
		return nil
	}
	if oldOK != newOK {
		return []string{context + " added or removed an enum boundary"}
	}
	oldSet, newSet := anySet(oldValues), anySet(newValues)
	if mode == requestDirection {
		for value := range oldSet {
			if !newSet[value] {
				return []string{context + " narrowed accepted enum values"}
			}
		}
	} else {
		for value := range newSet {
			if !oldSet[value] {
				return []string{context + " widened emitted enum values"}
			}
		}
	}
	return nil
}

func compareBounds(context string, oldSchema, newSchema map[string]any, mode direction) []string {
	var breaks []string
	for _, field := range []string{"minimum", "minLength", "minItems"} {
		oldValue, oldOK := number(oldSchema[field])
		newValue, newOK := number(newSchema[field])
		if mode == requestDirection && newOK && (!oldOK || newValue > oldValue) || mode == responseDirection && oldOK && (!newOK || newValue < oldValue) {
			breaks = append(breaks, context+" changed "+field+" incompatibly")
		}
	}
	for _, field := range []string{"maximum", "maxLength", "maxItems"} {
		oldValue, oldOK := number(oldSchema[field])
		newValue, newOK := number(newSchema[field])
		if mode == requestDirection && newOK && (!oldOK || newValue < oldValue) || mode == responseDirection && oldOK && (!newOK || newValue > oldValue) {
			breaks = append(breaks, context+" changed "+field+" incompatibly")
		}
	}
	return breaks
}

func compareAdditionalProperties(context string, oldRoot, newRoot map[string]any, oldRaw, newRaw any, mode direction, depth int) []string {
	oldAllowed, oldSchema := additionalProperties(oldRaw)
	newAllowed, newSchema := additionalProperties(newRaw)
	if mode == requestDirection && oldAllowed && !newAllowed {
		return []string{context + " stopped accepting additional properties"}
	}
	if mode == responseDirection && !oldAllowed && newAllowed {
		return []string{context + " started emitting unspecified properties"}
	}
	if oldSchema != nil {
		return compareSchema(context+" additional properties", oldRoot, newRoot, oldSchema, newSchema, mode, depth)
	}
	return nil
}

func additionalProperties(raw any) (bool, any) {
	if raw == nil {
		return true, nil
	}
	if value, ok := raw.(bool); ok {
		return value, nil
	}
	if _, ok := object(raw); ok {
		return true, raw
	}
	return false, nil
}

func resolve(root map[string]any, raw any) any {
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
		current = root
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

func expand(root map[string]any, raw any, depth int) any {
	if depth > 64 {
		return nil
	}
	raw = resolve(root, raw)
	switch value := raw.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			result[key] = expand(root, item, depth+1)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, item := range value {
			result[index] = expand(root, item, depth+1)
		}
		return result
	default:
		return raw
	}
}

func object(raw any) (map[string]any, bool) {
	value, ok := raw.(map[string]any)
	return value, ok
}

func boolean(raw any) bool {
	value, _ := raw.(bool)
	return value
}

func number(raw any) (float64, bool) {
	switch value := raw.(type) {
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	case float64:
		return value, true
	default:
		return 0, false
	}
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

func anySet(values []any) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		raw, _ := json.Marshal(value)
		result[string(raw)] = true
	}
	return result
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
