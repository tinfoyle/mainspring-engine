package accountexport

import (
	"slices"
	"strings"
)

type Disposition string

const (
	Included    Disposition = "included"
	Derived     Disposition = "derived"
	Operational Disposition = "operational"
	Secret      Disposition = "secret"
)

type TableCoverage struct {
	Schema      string      `json:"schema"`
	Table       string      `json:"table"`
	Section     string      `json:"section,omitempty"`
	Disposition Disposition `json:"disposition"`
	Reason      string      `json:"reason,omitempty"`
}

type Registry struct {
	sections map[string]Descriptor
	tables   map[string]TableCoverage
}

func NewRegistry(sections []Descriptor, tables []TableCoverage) (*Registry, error) {
	if len(sections) == 0 || len(tables) == 0 {
		return nil, ErrInvalid
	}
	registry := &Registry{sections: make(map[string]Descriptor, len(sections)), tables: make(map[string]TableCoverage, len(tables))}
	for _, descriptor := range sections {
		if !validDescriptor(descriptor) || registry.sections[descriptor.Code].Code != "" {
			return nil, ErrInvalid
		}
		descriptor.Stores = append([]string(nil), descriptor.Stores...)
		registry.sections[descriptor.Code] = descriptor
	}
	for _, coverage := range tables {
		key := coverage.Schema + "." + coverage.Table
		_, sectionExists := registry.sections[coverage.Section]
		validIncluded := coverage.Disposition == Included && sectionExists && coverage.Reason == ""
		validExcluded := slices.Contains([]Disposition{Derived, Operational, Secret}, coverage.Disposition) && coverage.Section == "" && validReason(coverage.Reason)
		if !validTableName(coverage.Schema, coverage.Table) || registry.tables[key].Table != "" || (!validIncluded && !validExcluded) {
			return nil, ErrInvalid
		}
		registry.tables[key] = coverage
	}
	return registry, nil
}

func (registry *Registry) Coverage(schema, table string) (TableCoverage, bool) {
	if registry == nil {
		return TableCoverage{}, false
	}
	value, found := registry.tables[schema+"."+table]
	return value, found
}

func (registry *Registry) Sections() []Descriptor {
	if registry == nil {
		return nil
	}
	result := make([]Descriptor, 0, len(registry.sections))
	for _, descriptor := range registry.sections {
		descriptor.Stores = append([]string(nil), descriptor.Stores...)
		result = append(result, descriptor)
	}
	slices.SortFunc(result, func(left, right Descriptor) int { return strings.Compare(left.Code, right.Code) })
	return result
}

func (registry *Registry) Tables() []TableCoverage {
	if registry == nil {
		return nil
	}
	result := make([]TableCoverage, 0, len(registry.tables))
	for _, coverage := range registry.tables {
		result = append(result, coverage)
	}
	slices.SortFunc(result, func(left, right TableCoverage) int {
		return strings.Compare(left.Schema+"."+left.Table, right.Schema+"."+right.Table)
	})
	return result
}

func (registry *Registry) validateSources(sections []SectionSource, objects []ObjectSource) error {
	if registry == nil || len(sections)+len(objects) != len(registry.sections) {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(registry.sections))
	validate := func(descriptor Descriptor) bool {
		expected, found := registry.sections[descriptor.Code]
		if !found || seen[descriptor.Code] || expected.SchemaVersion != descriptor.SchemaVersion || !slices.Equal(expected.Stores, descriptor.Stores) {
			return false
		}
		seen[descriptor.Code] = true
		return true
	}
	for _, source := range sections {
		if source == nil || !validate(source.Descriptor()) {
			return ErrInvalid
		}
	}
	for _, source := range objects {
		if source == nil || !validate(source.Descriptor()) {
			return ErrInvalid
		}
	}
	return nil
}

func validTableName(schema, table string) bool {
	return (schema == "public" || schema == "spyglass") && validCode.MatchString(table)
}

func validReason(reason string) bool {
	return len(reason) >= 10 && len(reason) <= 500 && strings.TrimSpace(reason) == reason && !strings.ContainsRune(reason, '\x00')
}
