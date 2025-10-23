package kine

import (
	"context"
	"fmt"
	"net"
	"time"

	kineconfig "github.com/k3s-io/kine/pkg/app"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
)

const (
	kineDialTimeout = 2 * time.Second
)

// Server represents a Kine server instance.
type Server struct {
	cfg *config.KommodityConfig
}

// NewServer creates a new Kine server instance.
func NewServer(cfg *config.KommodityConfig) *Server {
	return &Server{
		cfg: cfg,
	}
}

// StartKine starts a Kine server based on the provided Kommodity configuration.
func (ks *Server) StartKine() error {
	kineApp := kineconfig.New()

	err := kineApp.Run(append([]string{"kine"}, "--endpoint="+ks.cfg.DBURI.String()))
	if err != nil {
		return fmt.Errorf("failed to start kine: %w", err)
	}

	return nil
}

// WaitForKine waits until the Kine server is ready to accept TCP connections.
func (ks *Server) WaitForKine(ctx context.Context, readyChan chan struct{}) {
	go func() {
		logger := logging.FromContext(ctx)
		for {
			logger.Info("Waiting for Kine to be ready...")

			dialer := &net.Dialer{Timeout: kineDialTimeout}

			conn, err := dialer.DialContext(
				ctx,
				"tcp",
				ks.cfg.KineURI,
			)
			if err == nil {
				// Intentionally ignore close errors
				_ = conn.Close()

				close(readyChan)

				return
			}

			time.Sleep(kineDialTimeout)
		}
	}()
}
