package config

// Provider represents an infrastructure provider.
type Provider string

const (
	// ProviderCapiCore is the core Cluster API provider.
	ProviderCapiCore Provider = "capi"
	// ProviderDocker is the Docker infrastructure provider.
	ProviderDocker Provider = "docker"
	// ProviderTalos is the Talos infrastructure provider.
	ProviderTalos Provider = "talos"
	// ProviderAzure is the Azure infrastructure provider.
	ProviderAzure Provider = "azure"
	// ProviderScaleway is the Scaleway infrastructure provider.
	ProviderScaleway Provider = "scaleway"
)

// GetAllProviders returns a list of all supported providers without local development providers.
func GetAllProviders() []Provider {
	return []Provider{
		ProviderCapiCore,
		ProviderTalos,
		ProviderAzure,
		ProviderScaleway,
	}
}
