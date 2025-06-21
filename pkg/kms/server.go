// Package kms implements the Talos Linux KMS service, which provides
// a networked key management system for full disk encryption.
package kms

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/kommodity-io/kommodity/pkg/genericserver"
	"github.com/siderolabs/kms-client/api/kms"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

var (
	// ErrEmptyClientContext is an error that indicates the client context is empty.
	ErrEmptyClientContext = errors.New("client context is empty")
	// ErrEmptyData is an error that indicates the data is empty.
	ErrEmptyData = errors.New("data is empty")
)

const (
	pseudoSeal = "sealed:"
)

// ServiceServer is a struct that implements the ServiceServer interface.
type ServiceServer struct {
	kms.UnimplementedKMSServiceServer
}

// Seal is a method that encrypts data using the KMS service.
// DISCLAIMER: This is a mock implementation.
func (s *ServiceServer) Seal(ctx context.Context, req *kms.Request) (*kms.Response, error) {
	// We need the source IP for security hardening.
	client, ok := peer.FromContext(ctx)
	if !ok {
		return nil, ErrEmptyClientContext
	}

	host, _, err := net.SplitHostPort(client.Addr.String())
	if err != nil {
		return nil, fmt.Errorf("failed to extract client IP: %w", err)
	}

	// Check if the server is currently part of a cluster.
	// If part of cluster, check if it is online.
	// If online, and not in maintenance mode,
	// reject the request.

	// How does gRPC handle reverse proxying? What about trusted proxies?
	_ = host

	data := req.GetData()

	return &kms.Response{Data: append([]byte(pseudoSeal), data...)}, nil
}

// Unseal is a method that decrypts data using the KMS service.
// DISCLAIMER: This is a mock implementation.
func (s *ServiceServer) Unseal(_ context.Context, req *kms.Request) (*kms.Response, error) {
	data := req.GetData()
	if len(data) == 0 {
		return nil, fmt.Errorf("failed to unseal data: %w", ErrEmptyData)
	}

	return &kms.Response{Data: data[len(pseudoSeal):]}, nil
}

// NewGRPCServerFactory returns an initializer function that initializes the KMS service.
func NewGRPCServerFactory() genericserver.GRPCServerFactory {
	return func(srv *grpc.Server) error {
		// Create a new KMS service server and register it with the gRPC server.
		kms.RegisterKMSServiceServer(srv, &ServiceServer{})

		return nil
	}
}
