package azurearm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	armpolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/policy"
	armruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

const (
	armEndpoint   = "https://management.azure.com"
	armModuleName = "kommodity-azurearm"
	// armModuleVersion is a synthetic user-agent version for ARM requests.
	armModuleVersion = "v0.0.0"
	apiVersionQuery  = "api-version"

	// defaultRateLimitRequeue is used when ARM returns HTTP 429 but omits the
	// Retry-After header (defensive fallback).
	defaultRateLimitRequeue = 60 * time.Second
)

// armResponse is the minimal result of an ARM HTTP call. HTTP status codes are
// surfaced to the caller (including 404 and 429) rather than turned into errors,
// so the reconcile state machine can branch on them. retryAfter is populated on
// HTTP 429 responses.
type armResponse struct {
	statusCode int
	body       []byte
	retryAfter time.Duration
}

// armRequester is the minimal ARM-by-ID surface the reconciler depends on. It is
// satisfied by *armClient and allows the reconcile loop to be unit-tested with a
// fake implementation.
type armRequester interface {
	get(ctx context.Context, armID string, apiVersion string) (*armResponse, error)
	put(ctx context.Context, armID string, apiVersion string, ifMatch string, body any) (*armResponse, error)
	delete(ctx context.Context, armID string, apiVersion string) (*armResponse, error)
}

// armClient is a thin generic ARM-by-ID client built on the public azure-sdk-for-go
// pipeline. It mirrors the behaviour of ASO's internal genericarmclient (which we
// cannot import) for the create/get/delete operations the reconciler needs.
type armClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// newARMClient builds an ARM client from a token credential.
func newARMClient(cred azcore.TokenCredential) (*armClient, error) {
	opts := &armpolicy.ClientOptions{}

	pipeline, err := armruntime.NewPipeline(
		armModuleName, armModuleVersion, cred, runtime.PipelineOptions{}, opts)
	if err != nil {
		return nil, fmt.Errorf("building ARM pipeline: %w", err)
	}

	return &armClient{endpoint: armEndpoint, pipeline: pipeline}, nil
}

// GenericARMClient is the exported ARM-by-ID surface for out-of-package
// callers: method, ARM ID, api-version, optional If-Match preconditions, JSON
// body in/out.
type GenericARMClient interface {
	Get(
		ctx context.Context,
		armID string,
		apiVersion string,
	) (statusCode int, body []byte, retryAfter time.Duration, err error)
	Put(
		ctx context.Context,
		armID string,
		apiVersion string,
		ifMatch string,
		payload any,
	) (statusCode int, body []byte, retryAfter time.Duration, err error)
}

// GenericARMClientFactory builds a GenericARMClient from a service principal
// triple, so per-cluster credentials can be resolved at reconcile time.
type GenericARMClientFactory func(tenantID string, clientID string, clientSecret string) (GenericARMClient, error)

// NewGenericARMClient builds a generic ARM-by-ID client for callers outside
// this package (e.g. the Azure LB probe fixer) that need to read or mutate
// ARM resources CAPZ does not model as ASO CRs.
func NewGenericARMClient(cred azcore.TokenCredential) (GenericARMClient, error) {
	armClient, err := newARMClient(cred)
	if err != nil {
		return nil, err
	}

	return armClient, nil
}

// NewGenericARMClientFactory returns a factory building a GenericARMClient
// from a service principal triple, so per-cluster credentials can be resolved
// at reconcile time.
func NewGenericARMClientFactory() GenericARMClientFactory {
	return func(tenantID string, clientID string, clientSecret string) (GenericARMClient, error) {
		cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
		if err != nil {
			return nil, fmt.Errorf("building client secret credential: %w", err)
		}

		return NewGenericARMClient(cred)
	}
}

// Get implements GenericARMClient.
func (c *armClient) Get(
	ctx context.Context,
	armID string,
	apiVersion string,
) (int, []byte, time.Duration, error) {
	resp, err := c.get(ctx, armID, apiVersion)
	if err != nil {
		return 0, nil, 0, err
	}

	return resp.statusCode, resp.body, resp.retryAfter, nil
}

// Put implements GenericARMClient.
func (c *armClient) Put(
	ctx context.Context,
	armID string,
	apiVersion string,
	ifMatch string,
	payload any,
) (int, []byte, time.Duration, error) {
	resp, err := c.put(ctx, armID, apiVersion, ifMatch, payload)
	if err != nil {
		return 0, nil, 0, err
	}

	return resp.statusCode, resp.body, resp.retryAfter, nil
}

// put issues a PUT-by-ID with the given ARM body (marshaled to JSON). A
// non-empty ifMatch is sent as an If-Match precondition header, so the write
// only applies when the resource has not changed since the caller's GET
// (optimistic concurrency, the same mechanism CAPZ uses for its LB updates).
func (c *armClient) put(
	ctx context.Context,
	armID string,
	apiVersion string,
	ifMatch string,
	body any,
) (*armResponse, error) {
	req, err := c.newRequest(ctx, http.MethodPut, armID, apiVersion)
	if err != nil {
		return nil, err
	}

	if ifMatch != "" {
		req.Raw().Header.Set("If-Match", ifMatch)
	}

	// A []byte or json.RawMessage payload is already-marshaled JSON: set it
	// as the request body directly. runtime.MarshalAsJSON would run
	// json.Marshal on it, which base64-encodes a []byte into a JSON string —
	// ARM then receives a string instead of the resource object and answers
	// 400 on every PUT. Anything else is marshaled normally.
	var marshalErr error

	switch typed := body.(type) {
	case []byte:
		marshalErr = req.SetBody(streaming.NopCloser(bytes.NewReader(typed)), "application/json")
	case json.RawMessage:
		marshalErr = req.SetBody(streaming.NopCloser(bytes.NewReader(typed)), "application/json")
	default:
		marshalErr = runtime.MarshalAsJSON(req, body)
	}

	if marshalErr != nil {
		return nil, fmt.Errorf("marshaling ARM request body: %w", marshalErr)
	}

	return c.do(req)
}

// get issues a GET-by-ID. A 404 is returned as a response (statusCode 404), not an error.
func (c *armClient) get(ctx context.Context, armID string, apiVersion string) (*armResponse, error) {
	req, err := c.newRequest(ctx, http.MethodGet, armID, apiVersion)
	if err != nil {
		return nil, err
	}

	return c.do(req)
}

// delete issues a DELETE-by-ID.
func (c *armClient) delete(ctx context.Context, armID string, apiVersion string) (*armResponse, error) {
	req, err := c.newRequest(ctx, http.MethodDelete, armID, apiVersion)
	if err != nil {
		return nil, err
	}

	return c.do(req)
}

func (c *armClient) newRequest(
	ctx context.Context,
	method string,
	armID string,
	apiVersion string,
) (*policy.Request, error) {
	req, err := runtime.NewRequest(ctx, method, runtime.JoinPaths(c.endpoint, armID))
	if err != nil {
		return nil, fmt.Errorf("building ARM request: %w", err)
	}

	query := req.Raw().URL.Query()
	query.Set(apiVersionQuery, apiVersion)
	req.Raw().URL.RawQuery = query.Encode()
	req.Raw().Header.Set("Accept", "application/json")

	return req, nil
}

func (c *armClient) do(req *policy.Request) (*armResponse, error) {
	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("performing ARM request: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := runtime.Payload(resp)
	if err != nil {
		return nil, fmt.Errorf("reading ARM response body: %w", err)
	}

	result := &armResponse{statusCode: resp.StatusCode, body: body}

	if resp.StatusCode == http.StatusTooManyRequests {
		result.retryAfter = parseRetryAfter(resp.Header)
	}

	return result, nil
}

// parseRetryAfter extracts the Retry-After delay from an HTTP header. It handles
// the integer-seconds form (most common for Azure ARM); on parse failure it falls
// back to defaultRateLimitRequeue.
func parseRetryAfter(header http.Header) time.Duration {
	value := header.Get("Retry-After")
	if value == "" {
		return defaultRateLimitRequeue
	}

	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return defaultRateLimitRequeue
	}

	return time.Duration(seconds) * time.Second
}
