package reconciler

import "github.com/kommodity-io/kommodity/pkg/config"

// FactoryImpl is the default reconciler implementation of Factory.
type FactoryImpl struct {
	providerMap map[config.Provider]Module
}

// NewReconcilerFactory creates a new ReconcilerFactory.
func NewReconcilerFactory() *FactoryImpl {
	return &FactoryImpl{
		providerMap: map[config.Provider]Module{
			config.ProviderCapiCore: NewCoreModule(RemoteConnectionGracePeriod),
			config.ProviderAzure:    NewAzureModule(),
			config.ProviderTalos:    NewTalosModule(),
			config.ProviderDocker:   NewDockerModule(),
			config.ProviderScaleway: NewScalewayModule(),
		},
	}
}

// Build builds the list of Modules to set up based on config.
func (f *FactoryImpl) Build(cfg *config.KommodityConfig) (map[config.Provider]Module, error) {
	modules := make(map[config.Provider]Module)

	for _, provider := range cfg.InfrastructureProviders {
		module, exists := f.providerMap[provider]
		if !exists {
			return nil, ErrUnsupportedProvider
		}

		modules[provider] = module
	}

	return modules, nil
}
