package registry

import (
	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/gsuite"
	"mcpyeahyouknowme/sources/whatsapp"
)

// Descriptor centralizes construction and auth checks for a data source.
type Descriptor struct {
	Name            string
	New             func(dataDir string) core.DataSource
	IsAuthenticated func(dataDir string) bool
	IndexGlobally   bool
	RunsCore        bool
}

// All contains every source known to the application.
var All = []Descriptor{
	{
		Name:            "whatsapp",
		New:             func(dataDir string) core.DataSource { return whatsapp.NewSource(dataDir) },
		IsAuthenticated: whatsapp.IsLoggedIn,
		IndexGlobally:   true,
		RunsCore:        true,
	},
	{
		Name:            "gsuite",
		New:             func(dataDir string) core.DataSource { return gsuite.NewSource(dataDir) },
		IsAuthenticated: gsuite.IsLoggedIn,
		IndexGlobally:   true,
		RunsCore:        true,
	},
}

// Find looks up a source descriptor by name.
func Find(name string) (Descriptor, bool) {
	for _, desc := range All {
		if desc.Name == name {
			return desc, true
		}
	}
	return Descriptor{}, false
}

// NewSource constructs a source by name.
func NewSource(name, dataDir string) core.DataSource {
	desc, ok := Find(name)
	if !ok {
		return nil
	}
	return desc.New(dataDir)
}

// IsAuthenticated reports whether a source has usable credentials.
func IsAuthenticated(name, dataDir string) bool {
	desc, ok := Find(name)
	if !ok || desc.IsAuthenticated == nil {
		return true
	}
	return desc.IsAuthenticated(dataDir)
}

// LoadAll constructs all registered sources.
func LoadAll(dataDir string) []core.DataSource {
	sources := make([]core.DataSource, 0, len(All))
	for _, desc := range All {
		sources = append(sources, desc.New(dataDir))
	}
	return sources
}

// Names returns the registered source names in descriptor order.
func Names() []string {
	names := make([]string, 0, len(All))
	for _, desc := range All {
		names = append(names, desc.Name)
	}
	return names
}
