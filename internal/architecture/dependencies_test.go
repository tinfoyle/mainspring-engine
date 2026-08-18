package architecture_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/tinfoyle/spyglass-engine/"

func TestProductionDependencyDirection(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	for _, tree := range []string{"cmd", "internal", "migrations"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			checkFileImports(t, root, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", tree, err)
		}
	}
}

func checkFileImports(t *testing.T, root, path string) {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	relative, _ := filepath.Rel(root, path)
	sourceLayer := layer(filepath.ToSlash(relative))
	for _, spec := range parsed.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !strings.HasPrefix(importPath, modulePath) {
			continue
		}
		projectPath := strings.TrimPrefix(importPath, modulePath)
		if strings.HasPrefix(projectPath, "prototype/") {
			t.Errorf("%s imports prototype code %s", relative, importPath)
			continue
		}
		targetLayer := layer(projectPath)
		if forbidden(sourceLayer, targetLayer) {
			t.Errorf("%s violates dependency direction: %s -> %s (%s)", relative, sourceLayer, targetLayer, importPath)
		}
	}
}

func layer(path string) string {
	for _, candidate := range []string{"internal/modules/", "internal/platform/", "internal/application/", "internal/adapters/", "internal/transport/", "internal/bootstrap/"} {
		if strings.HasPrefix(path, candidate) {
			return strings.TrimSuffix(strings.TrimPrefix(candidate, "internal/"), "/")
		}
	}
	if strings.HasPrefix(path, "cmd/") {
		return "cmd"
	}
	return "other"
}

func forbidden(source, target string) bool {
	denied := map[string]map[string]bool{
		"modules":     {"application": true, "adapters": true, "transport": true, "bootstrap": true},
		"platform":    {"modules": true, "application": true, "adapters": true, "transport": true, "bootstrap": true},
		"application": {"adapters": true, "transport": true, "bootstrap": true},
		"adapters":    {"transport": true, "bootstrap": true},
		"transport":   {"adapters": true, "bootstrap": true},
	}
	return denied[source][target]
}
