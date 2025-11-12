package test_test

import (
	"os"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/test/apirequests"
	"github.com/kommodity-io/kommodity/pkg/test/helpers"
)

var env helpers.TestEnvironment

func TestMain(m *testing.M) {
	// --- Setup ---
	env = helpers.SetupContainers()

	// Run tests
	code := m.Run()

	// --- Teardown ---
	env.Teardown()

	os.Exit(code)
}

func TestAPIIntegration(t *testing.T) {
	t.Parallel()
	newRequest := apirequests.APIRequest{
		TestEnvironment:    env,
		Endpoint:           "/api",
		Type:               "GET",
		ExpectedStatusCode: 200,
		ExpectedResponseBody: `{
					"kind": "APIVersions",
					"versions": [
						"v1"
					],
					"serverAddressByClientCIDRs": [
						{
						"clientCIDR": "0.0.0.0/0",
						"serverAddress": ":8443"
						}
					]
					}`,
	}

	newRequest.RunRequest(t)
}
