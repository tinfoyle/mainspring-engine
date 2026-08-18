// Package buildinfo exposes immutable release identity injected by the image build.
package buildinfo

type Info struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
	BuiltAt  string `json:"built_at"`
}

// These defaults keep local go run builds explicit. Release builds replace all
// three through -ldflags -X and bind them to OCI labels and provenance.
var (
	Version  = "development"
	Revision = "unknown"
	BuiltAt  = "unknown"
)

func Current() Info {
	return Info{Version: Version, Revision: Revision, BuiltAt: BuiltAt}
}
