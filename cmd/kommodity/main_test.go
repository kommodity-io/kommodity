package main_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	main "github.com/kommodity-io/kommodity/cmd/kommodity"
	taloskms "github.com/siderolabs/kms-client/api/kms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection/grpc_reflection_v1"
)

func TestKMSService(t *testing.T) {
	// Arrange.
	port := "50051"
	t.Setenv("PORT", port)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start the server in a goroutine
	go func() {
		main.NewServer(ctx).ListenAndServe(ctx)
	}()

	// Query the /health endpoint until we get a 200 response.
	retries := 0
	for range retries {
		if retries > 10 {
			t.Fatalf("Server failed to start")
		}

		resp, err := http.Get("http://localhost:" + port + "/health")
		require.NoError(t, err, "Failed to query /health endpoint")

		if resp.StatusCode == http.StatusOK {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Create a client connection.
	conn, err := grpc.NewClient("localhost:"+port, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "Failed to connect to server")

	t.Cleanup(func() {
		conn.Close()
	})

	// Create a client.
	client := taloskms.NewKMSServiceClient(conn)

	// Act: Test Seal
	sealReq := &taloskms.Request{
		Data: []byte("test data"),
	}
	sealResp, err := client.Seal(ctx, sealReq)

	// Assert: Seal.
	require.NoError(t, err, "Seal failed")
	assert.Equal(t, "sealed:test data", string(sealResp.Data), "Unexpected seal response")

	// Act: Test Unseal.
	unsealReq := &taloskms.Request{
		Data: sealResp.Data,
	}
	unsealResp, err := client.Unseal(ctx, unsealReq)

	// Assert: Unseal.
	require.NoError(t, err, "Unseal failed")
	assert.Equal(t, "test data", string(unsealResp.Data), "Unexpected unseal response")

	// Arrange: Test reflection.
	reflectionClient := grpc_reflection_v1.NewServerReflectionClient(conn)

	stream, err := reflectionClient.ServerReflectionInfo(ctx)
	require.NoError(t, err, "Getting reflection info should not fail")

	// Act: Request list of services
	if err := stream.Send(&grpc_reflection_v1.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1.ServerReflectionRequest_ListServices{},
	}); err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	resp, err := stream.Recv()
	require.NoError(t, err, "Receiving reflection response should not fail")

	// Assert: Reflection.
	serviceNames := make([]string, len(resp.GetListServicesResponse().Service))
	for i, service := range resp.GetListServicesResponse().Service {
		serviceNames[i] = service.Name
	}

	assert.Contains(t, serviceNames, "grpc.reflection.v1.ServerReflection", "ServerReflection service should be listed")
}
