package api

// Version is the current version of Kommodity.
// This should be set via build flags in production:
// -ldflags "-X github.com/kommodity-io/kommodity/pkg/ui/api.Version=v1.2.3".
var Version = "development" //nolint:gochecknoglobals // Version is set via build flags

// GetKommodityVersion returns the current Kommodity version.
func GetKommodityVersion() string {
	return Version
}
