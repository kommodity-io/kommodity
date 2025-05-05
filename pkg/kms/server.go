package kms

import (
	"context"

	"github.com/siderolabs/kms-client/api/kms"
)

type KMSServiceServer struct {
	kms.UnimplementedKMSServiceServer
}

func (s *KMSServiceServer) Seal(ctx context.Context, req *kms.Request) (*kms.Response, error) {
	// Example implementation
	return &kms.Response{Data: append([]byte("sealed:"), req.Data...)}, nil
}

func (s *KMSServiceServer) Unseal(ctx context.Context, req *kms.Request) (*kms.Response, error) {
	// Example implementation
	return &kms.Response{Data: req.Data[len("sealed:"):]}, nil
}
