package kms

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/siderolabs/kms-client/api/kms"
	"google.golang.org/grpc/peer"
)

var (
	// ErrEmptyClientContext is an error that indicates the client context is empty.
	ErrEmptyClientContext = errors.New("client context is empty")
)

// KMSServiceServer is a struct that implements the KMSServiceServer interface.
type KMSServiceServer struct {
	kms.UnimplementedKMSServiceServer
}

// Seal is a method encrypts data using the KMS service.
func (s *KMSServiceServer) Seal(ctx context.Context, req *kms.Request) (*kms.Response, error) {
	// We need the source IP for security hardening.
	client, ok := peer.FromContext(ctx)
	if !ok {
		return nil, ErrEmptyClientContext
	}

	host, _, err := net.SplitHostPort(client.Addr.String())
	if err != nil {
		return nil, fmt.Errorf("failed to extract client IP: %w", err)
	}

	// TODO: Check if the server is currently part of a cluster.
	// TODO: If part of cluster, check if it is online. If online, and not in maintenance mode,
	//       reject the request.
	// TODO: How does gRPC handle reverse proxying? What about trusted proxies?
	_ = host

	// Example implementation
	return &kms.Response{Data: append([]byte("sealed:"), req.Data...)}, nil
}

func (s *KMSServiceServer) Unseal(ctx context.Context, req *kms.Request) (*kms.Response, error) {
	// Example implementation
	return &kms.Response{Data: req.Data[len("sealed:"):]}, nil
}
