package kine

import (
	"fmt"

	kineconfig "github.com/k3s-io/kine/pkg/app"
	"github.com/kommodity-io/kommodity/pkg/config"
)

// StartKine starts a Kine server based on the provided Kommodity configuration.
func StartKine(cfg *config.KommodityConfig) error {
	kineApp := kineconfig.New()

	err := kineApp.Run(append([]string{"kine"}, "--endpoint="+cfg.DBURI.String()))
	if err != nil {
		return fmt.Errorf("failed to start kine: %w", err)
	}

	return nil
}
