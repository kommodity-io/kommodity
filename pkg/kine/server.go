package kine

import (
	"context"
	"fmt"
	"os"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc/credentials/insecure"

	kineconfig "github.com/k3s-io/kine/pkg/app"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"google.golang.org/grpc"
)

const (
	kineDialTimeout         = 2 * time.Second
	binDirectoryPermissions = 0750
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
	// Ensure bin folder exists
	err := os.MkdirAll("bin", binDirectoryPermissions)
	if err != nil {
		return fmt.Errorf("failed to create bin directory: %w", err)
	}

	kineApp := kineconfig.New()

	err = kineApp.Run([]string{
		"kine",
		"--listen-address=" + ks.cfg.KineURI,
		"--endpoint=" + ks.cfg.DBURI.String(),
		"--metrics-bind-address=0",
	})
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
			logger.Info("Waiting for Kine to be ready (grpc health check)...")

			cli, err := clientv3.New(clientv3.Config{
				Endpoints:   []string{ks.cfg.KineURI},
				DialTimeout: kineDialTimeout,
				DialOptions: []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
			})
			if err == nil {
				_, err := cli.Get(ctx, "health-check")
				_ = cli.Close()

				if err == nil {
					close(readyChan)

					return
				}
			}

			time.Sleep(kineDialTimeout)
		}
	}()
}
