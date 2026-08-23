// Command apicontract validates the customer API OpenAPI source against Go
// transport registrations and emits bounded route inventories for Go and web.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const contractPath = "api/spyglass.openapi.json"

var methods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}

type document struct {
	OpenAPI    string                                `json:"openapi"`
	Info       json.RawMessage                       `json:"info"`
	Servers    json.RawMessage                       `json:"servers"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components json.RawMessage                       `json:"components"`
}

type operation struct {
	ID        string          `json:"operationId"`
	Service   string          `json:"x-spyglass-service"`
	Security  json.RawMessage `json:"security"`
	Responses json.RawMessage `json:"responses"`
}

type route struct {
	Service, Method, Path, OperationID, Authentication, Contract string
}

func main() {
	write := flag.Bool("write", false, "write generated route inventories")
	check := flag.Bool("check", false, "verify generated files and Go transport registrations")
	flag.Parse()
	if *write == *check || flag.NArg() != 0 {
		fatal(errors.New("usage: go run ./cmd/apicontract -write|-check"))
	}
	root, err := repositoryRoot()
	if err != nil {
		fatal(err)
	}
	routes, err := loadContract(filepath.Join(root, contractPath))
	if err != nil {
		fatal(err)
	}
	schemas, err := loadSchemas(filepath.Join(root, contractPath))
	if err != nil {
		fatal(err)
	}
	actual, err := registeredRoutes(root)
	if err != nil {
		fatal(err)
	}
	if err := compareRoutes(routes, actual); err != nil {
		fatal(err)
	}
	outputs := map[string][]byte{
		"internal/generated/apicontract/routes.go": renderGo(routes),
		"website/lib/generated/api-contract.ts":    renderTypeScript(routes),
		"website/lib/generated/api-types.ts":       renderTypeScriptSchemas(schemas),
	}
	for path, content := range outputs {
		path = filepath.Join(root, path)
		if *write {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				fatal(err)
			}
			if err := os.WriteFile(path, content, 0o644); err != nil {
				fatal(err)
			}
			continue
		}
		stored, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(stored, content) {
			fatal(fmt.Errorf("generated API contract %s is stale; run go run ./cmd/apicontract -write", path))
		}
	}
}

func loadSchemas(path string) (map[string]json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var source struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("decode OpenAPI schemas: %w", err)
	}
	if len(source.Components.Schemas) == 0 {
		return nil, errors.New("OpenAPI component schemas are required")
	}
	return source.Components.Schemas, nil
}

func loadContract(path string) ([]route, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var source document
	if err := decoder.Decode(&source); err != nil {
		return nil, fmt.Errorf("decode OpenAPI source: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("OpenAPI source must contain exactly one JSON document")
	}
	if source.OpenAPI != "3.1.0" || len(source.Info) == 0 || len(source.Paths) == 0 || len(source.Components) == 0 {
		return nil, errors.New("OpenAPI 3.1 source with paths is required")
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, err
	}
	if err := validateReferences(tree, tree); err != nil {
		return nil, err
	}
	seenIDs := map[string]bool{}
	routes := make([]route, 0)
	for path, item := range source.Paths {
		if !strings.HasPrefix(path, "/api/v1/") && path != "/webhooks/stripe" {
			return nil, fmt.Errorf("unsupported customer API path %q", path)
		}
		if err := validatePathParameters(path, item["parameters"]); err != nil {
			return nil, err
		}
		for method, raw := range item {
			method = strings.ToLower(method)
			if !methods[method] {
				continue
			}
			var op operation
			if err := json.Unmarshal(raw, &op); err != nil {
				return nil, fmt.Errorf("decode %s %s: %w", method, path, err)
			}
			if op.ID == "" || seenIDs[op.ID] || (op.Service != "account-api" && op.Service != "cell-api") || op.Security == nil || op.Responses == nil {
				return nil, fmt.Errorf("operation %s %s lacks a unique ID, owner, security, or responses", method, path)
			}
			seenIDs[op.ID] = true
			auth := "public"
			if string(op.Security) != "[]" {
				var requirements []map[string][]string
				if err := json.Unmarshal(op.Security, &requirements); err != nil || len(requirements) != 1 || len(requirements[0]) != 1 {
					return nil, fmt.Errorf("operation %s has an invalid security declaration", op.ID)
				}
				for name := range requirements[0] {
					auth = name
				}
			}
			if auth != "public" && auth != "sessionCookie" && auth != "stripeSignature" && auth != "exportDownloadCapability" {
				return nil, fmt.Errorf("operation %s has unsupported authentication %q", op.ID, auth)
			}
			if (path == "/webhooks/stripe") != (auth == "stripeSignature") ||
				(path == "/api/v1/account-exports/{exportID}/artifact") != (auth == "exportDownloadCapability") ||
				(op.Service == "cell-api" && auth != "sessionCookie") {
				return nil, fmt.Errorf("operation %s authentication does not match its boundary", op.ID)
			}
			contract := "typed"
			if bytes.Contains(op.Responses, []byte("#/components/responses/Success")) {
				contract = "route"
			}
			routes = append(routes, route{Service: op.Service, Method: strings.ToUpper(method), Path: path, OperationID: op.ID, Authentication: auth, Contract: contract})
		}
	}
	sortRoutes(routes)
	return routes, nil
}

func validateReferences(value, root any) error {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if err := validateReferences(item, root); err != nil {
				return err
			}
		}
	case map[string]any:
		if raw, exists := typed["$ref"]; exists {
			reference, ok := raw.(string)
			if !ok || !strings.HasPrefix(reference, "#/") || resolveReference(root, reference) == nil {
				return fmt.Errorf("OpenAPI reference %v is invalid or unresolved", raw)
			}
		}
		for _, item := range typed {
			if err := validateReferences(item, root); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveReference(root any, reference string) any {
	current := root
	for _, raw := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		name := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = object[name]
		if !ok {
			return nil
		}
	}
	return current
}

func validatePathParameters(path string, raw json.RawMessage) error {
	expected := map[string]bool{}
	remainder := path
	for {
		start := strings.IndexByte(remainder, '{')
		if start < 0 {
			break
		}
		end := strings.IndexByte(remainder[start+1:], '}')
		if end < 0 {
			return fmt.Errorf("path %q has an unterminated parameter", path)
		}
		name := remainder[start+1 : start+1+end]
		if name == "" || expected[name] {
			return fmt.Errorf("path %q has an invalid parameter", path)
		}
		expected[name] = true
		remainder = remainder[start+end+2:]
	}
	type reference struct {
		Ref string `json:"$ref"`
	}
	declared := map[string]bool{}
	if len(raw) > 0 {
		var values []reference
		if err := json.Unmarshal(raw, &values); err != nil {
			return fmt.Errorf("path %q parameters are invalid", path)
		}
		for _, value := range values {
			const prefix = "#/components/parameters/"
			if !strings.HasPrefix(value.Ref, prefix) || declared[strings.TrimPrefix(value.Ref, prefix)] {
				return fmt.Errorf("path %q parameter reference is invalid", path)
			}
			declared[strings.TrimPrefix(value.Ref, prefix)] = true
		}
	}
	matched := len(expected) == len(declared)
	for name := range expected {
		matched = matched && declared[name]
	}
	if !matched {
		return fmt.Errorf("path %q parameters do not match template: expected=%v declared=%v", path, expected, declared)
	}
	return nil
}

func registeredRoutes(root string) ([]route, error) {
	files := map[string]string{
		"account-api": "internal/transport/httpapi/server.go",
		"cell-api":    "internal/transport/cellapi/server.go",
	}
	routes := make([]route, 0)
	for service, path := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, path), nil, 0)
		if err != nil {
			return nil, err
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			literal, literalOK := call.Args[0].(*ast.BasicLit)
			if !ok || selector.Sel.Name != "HandleFunc" || !literalOK || literal.Kind != token.STRING {
				return true
			}
			pattern, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			method, path, found := strings.Cut(pattern, " ")
			if !found || (!strings.HasPrefix(path, "/api/v1/") && path != "/webhooks/stripe") {
				return true
			}
			routes = append(routes, route{Service: service, Method: method, Path: path})
			return true
		})
	}
	sortRoutes(routes)
	return routes, nil
}

func repositoryRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errors.New("repository root containing go.mod was not found")
		}
		directory = parent
	}
}

func compareRoutes(contract, actual []route) error {
	expected := make(map[string]route, len(contract))
	for _, value := range contract {
		expected[routeKey(value)] = value
	}
	registered := make(map[string]route, len(actual))
	for _, value := range actual {
		registered[routeKey(value)] = value
	}
	missing, undocumented := make([]string, 0), make([]string, 0)
	for key := range expected {
		if _, ok := registered[key]; !ok {
			missing = append(missing, key)
		}
	}
	for key := range registered {
		if _, ok := expected[key]; !ok {
			undocumented = append(undocumented, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(undocumented)
	if len(missing) > 0 || len(undocumented) > 0 {
		return fmt.Errorf("API route drift: missing registrations=%v undocumented registrations=%v", missing, undocumented)
	}
	return nil
}

func renderGo(routes []route) []byte {
	var output strings.Builder
	output.WriteString("// Code generated by cmd/apicontract; DO NOT EDIT.\npackage apicontract\n\n")
	output.WriteString("type Route struct { Service, Method, Path, OperationID, Authentication, Contract string }\n\nvar Routes = [...]Route{\n")
	for _, value := range routes {
		fmt.Fprintf(&output, "\t{Service: %q, Method: %q, Path: %q, OperationID: %q, Authentication: %q, Contract: %q},\n", value.Service, value.Method, value.Path, value.OperationID, value.Authentication, value.Contract)
	}
	output.WriteString("}\n")
	formatted, err := format.Source([]byte(output.String()))
	if err != nil {
		panic(err)
	}
	return formatted
}

func renderTypeScript(routes []route) []byte {
	var output strings.Builder
	output.WriteString("// Code generated by cmd/apicontract; DO NOT EDIT.\nexport const apiRoutes = [\n")
	for _, value := range routes {
		fmt.Fprintf(&output, "  { service: %q, method: %q, path: %q, operationId: %q, authentication: %q, contract: %q },\n", value.Service, value.Method, value.Path, value.OperationID, value.Authentication, value.Contract)
	}
	output.WriteString("] as const;\n\nexport type ApiRoute = (typeof apiRoutes)[number];\nexport type ApiOperationId = ApiRoute[\"operationId\"];\n")
	return []byte(output.String())
}

type jsonSchema struct {
	Ref                  string                     `json:"$ref"`
	Type                 string                     `json:"type"`
	Enum                 []string                   `json:"enum"`
	AnyOf                []json.RawMessage          `json:"anyOf"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Required             []string                   `json:"required"`
	Items                json.RawMessage            `json:"items"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
}

func renderTypeScriptSchemas(schemas map[string]json.RawMessage) []byte {
	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	var output strings.Builder
	output.WriteString("// Code generated by cmd/apicontract; DO NOT EDIT.\n")
	for _, name := range names {
		var schema jsonSchema
		if err := json.Unmarshal(schemas[name], &schema); err != nil {
			panic(fmt.Errorf("decode schema %s: %w", name, err))
		}
		if schema.Type == "object" && len(schema.Properties) > 0 {
			fmt.Fprintf(&output, "\nexport interface %s {\n", name)
			writeTypeScriptProperties(&output, schema)
			output.WriteString("}\n")
			continue
		}
		fmt.Fprintf(&output, "\nexport type %s = %s;\n", name, typeScriptSchemaType(schemas[name]))
	}
	output.WriteString("\nexport interface ApiSchemas {\n")
	for _, name := range names {
		fmt.Fprintf(&output, "  readonly %s: %s;\n", name, name)
	}
	output.WriteString("}\n")
	return []byte(output.String())
}

func writeTypeScriptProperties(output *strings.Builder, schema jsonSchema) {
	required := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = true
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		optional := "?"
		if required[name] {
			optional = ""
		}
		fmt.Fprintf(output, "  readonly %s%s: %s;\n", strconv.Quote(name), optional, typeScriptSchemaType(schema.Properties[name]))
	}
}

func typeScriptSchemaType(raw json.RawMessage) string {
	var schema jsonSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		panic(fmt.Errorf("decode inline schema: %w", err))
	}
	if schema.Ref != "" {
		const prefix = "#/components/schemas/"
		if !strings.HasPrefix(schema.Ref, prefix) || strings.TrimPrefix(schema.Ref, prefix) == "" {
			panic(fmt.Errorf("unsupported TypeScript schema reference %q", schema.Ref))
		}
		return strings.TrimPrefix(schema.Ref, prefix)
	}
	if len(schema.AnyOf) > 0 {
		values := make([]string, 0, len(schema.AnyOf))
		for _, item := range schema.AnyOf {
			values = append(values, typeScriptSchemaType(item))
		}
		return strings.Join(values, " | ")
	}
	if len(schema.Enum) > 0 {
		values := make([]string, 0, len(schema.Enum))
		for _, value := range schema.Enum {
			values = append(values, strconv.Quote(value))
		}
		return strings.Join(values, " | ")
	}
	switch schema.Type {
	case "string":
		return "string"
	case "integer", "number":
		return "number"
	case "boolean":
		return "boolean"
	case "null":
		return "null"
	case "array":
		if len(schema.Items) == 0 {
			panic(errors.New("array schema requires items"))
		}
		return "ReadonlyArray<" + typeScriptSchemaType(schema.Items) + ">"
	case "object":
		if len(schema.Properties) > 0 {
			var output strings.Builder
			output.WriteString("{\n")
			writeTypeScriptProperties(&output, schema)
			output.WriteString("}")
			return output.String()
		}
		if len(schema.AdditionalProperties) == 0 || string(schema.AdditionalProperties) == "true" {
			return "Readonly<Record<string, unknown>>"
		}
		if string(schema.AdditionalProperties) != "false" {
			return "Readonly<Record<string, " + typeScriptSchemaType(schema.AdditionalProperties) + ">>"
		}
		return "Readonly<Record<string, never>>"
	default:
		panic(fmt.Errorf("unsupported JSON Schema type %q", schema.Type))
	}
}

func sortRoutes(routes []route) {
	sort.Slice(routes, func(i, j int) bool { return routeKey(routes[i]) < routeKey(routes[j]) })
}

func routeKey(value route) string { return value.Service + " " + value.Method + " " + value.Path }

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
