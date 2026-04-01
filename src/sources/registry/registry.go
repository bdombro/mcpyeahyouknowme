package registry

import (
	"mcpyeahyouknowme/core"
	"mcpyeahyouknowme/sources/google_places"
	"mcpyeahyouknowme/sources/gsuite"
	"mcpyeahyouknowme/sources/whatsapp"
)

// Descriptor centralizes construction and auth checks for a data source.
type Descriptor struct {
	Name              string
	New               func(dataDir string) core.DataSource
	IsEnabled         func() bool
	UnavailableReason string
	IsAuthenticated   func(dataDir string) bool
	IndexGlobally     bool
	RunsCore          bool
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
		Name:              "gsuite",
		New:               func(dataDir string) core.DataSource { return gsuite.NewSource(dataDir) },
		IsEnabled:         gsuite.IsConfigured,
		UnavailableReason: "missing build-time GOOGLE_CLIENT_ID and/or GOOGLE_CLIENT_SECRET",
		IsAuthenticated:   gsuite.IsLoggedIn,
		IndexGlobally:     true,
		RunsCore:          true,
	},
	{
		Name:              "google_places",
		New:               func(dataDir string) core.DataSource { return google_places.NewSource(dataDir) },
		IsEnabled:         google_places.IsConfigured,
		UnavailableReason: "missing build-time GOOGLE_PLACE_API_KEY",
		IsAuthenticated:   func(dataDir string) bool { return google_places.IsConfigured() },
		IndexGlobally:     false,
		RunsCore:          false,
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

// IsAvailable reports whether a source was built/configured with its required
// build-time credentials or feature flags.
func IsAvailable(name string) (bool, string) {
	desc, ok := Find(name)
	if !ok {
		return false, ""
	}
	if desc.IsEnabled == nil || desc.IsEnabled() {
		return true, ""
	}
	return false, desc.UnavailableReason
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
