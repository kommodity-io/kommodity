package server_test

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/kommodity-io/kommodity/pkg/server"
	taloskms "github.com/siderolabs/kms-client/api/kms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection/grpc_reflection_v1"
)

func randomPort(t *testing.T) string {
	t.Helper()

	offset, err := rand.Int(rand.Reader, big.NewInt(1000))
	require.NoError(t, err, "Failed to generate random offset")

	return strconv.FormatInt(55000+offset.Int64(), 10)
}

func setupTestServer(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)

	srv, err := server.New(ctx)
	require.NoError(t, err, "should create server without error")

	// Start the server in a goroutine
	go func() {
		err := srv.ListenAndServe(ctx)
		if err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("Server failed to start: %s", err)
			}
		}

		t.Cleanup(func() {
			err := srv.Shutdown(ctx)
			if err != nil {
				t.Errorf("Failed to shutdown server: %s", err)
			}
		})
	}()

	return ctx
}

func TestSeal(t *testing.T) {
	port := randomPort(t)

	t.Setenv("PORT", port)

	ctx := setupTestServer(t)

	// Arrange: Create a client connection.
	conn, err := grpc.NewClient("localhost:"+port, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "Failed to connect to server")

	t.Cleanup(func() {
		err := conn.Close()
		if err != nil {
			t.Errorf("Failed to close connection: %s", err)
		}
	})

	// Arrange: Create a client.
	client := taloskms.NewKMSServiceClient(conn)

	// Act: Test Seal
	sealReq := &taloskms.Request{
		Data: []byte("test data"),
	}
	sealResp, err := client.Seal(ctx, sealReq)

	// Assert: Seal.
	require.NoError(t, err, "Seal failed")
	assert.Equal(t, "sealed:test data", string(sealResp.GetData()), "Unexpected seal response")
}

// TestUnseal tests the Unseal method of the KMS service.
func TestUnseal(t *testing.T) {
	port := randomPort(t)

	t.Setenv("PORT", port)

	ctx := setupTestServer(t)

	// Arrange: Create a client connection.
	conn, err := grpc.NewClient("localhost:"+port, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "Failed to connect to server")

	t.Cleanup(func() {
		err := conn.Close()
		if err != nil {
			t.Errorf("Failed to close connection: %s", err)
		}
	})

	// Arrange: Create a client.
	client := taloskms.NewKMSServiceClient(conn)

	// Act: Test Unseal
	unsealReq := &taloskms.Request{
		Data: []byte("sealed:test data"),
	}
	unsealResp, err := client.Unseal(ctx, unsealReq)

	// Assert: Unseal.
	require.NoError(t, err, "Unseal failed")
	assert.Equal(t, "test data", string(unsealResp.GetData()), "Unexpected unseal response")
}

// TestReflection tests the reflection service of the KMS server.
func TestReflection(t *testing.T) {
	port := randomPort(t)

	t.Setenv("PORT", port)

	ctx := setupTestServer(t)

	// Arrange: Create a client connection.
	conn, err := grpc.NewClient("localhost:"+port, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "Failed to connect to server")

	t.Cleanup(func() {
		err := conn.Close()
		if err != nil {
			t.Errorf("Failed to close connection: %s", err)
		}
	})

	// Arrange: Create a reflection client.
	reflectionClient := grpc_reflection_v1.NewServerReflectionClient(conn)

	stream, err := reflectionClient.ServerReflectionInfo(ctx)
	require.NoError(t, err, "Getting reflection info should not fail")

	// Act: Request list of services
	err = stream.Send(&grpc_reflection_v1.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1.ServerReflectionRequest_ListServices{},
	})
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	resp, err := stream.Recv()
	require.NoError(t, err, "Receiving reflection response should not fail")

	// Assert: Reflection.
	serviceNames := make([]string, len(resp.GetListServicesResponse().GetService()))
	for i, service := range resp.GetListServicesResponse().GetService() {
		serviceNames[i] = service.GetName()
	}

	assert.Contains(t, serviceNames, "grpc.reflection.v1.ServerReflection", "ServerReflection service should be listed")
}
