package agent

import "fmt"

func NewProvider(name, codexBinary string) (Provider, error) {
	switch name {
	case "mock", "":
		return MockProvider{}, nil
	case "codex", "codex-cli":
		return NewCodexProvider(codexBinary), nil
	default:
		return nil, fmt.Errorf("unknown agent provider %q", name)
	}
}
