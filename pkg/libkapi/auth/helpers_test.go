package auth_test

import (
	"context"
	"net/http"

	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/authorization/authorizer"
)

// fakeAuthenticator is a test authenticator that returns a configurable
// result and tracks call count.
type fakeAuthenticator struct {
	ok        bool
	callCount int
	response  *authenticator.Response
}

func (f *fakeAuthenticator) AuthenticateRequest(_ *http.Request) (*authenticator.Response, bool, error) {
	f.callCount++

	if f.ok {
		if f.response != nil {
			return f.response, true, nil
		}

		return &authenticator.Response{
			User: &user.DefaultInfo{Name: "fake-user"},
		}, true, nil
	}

	return nil, false, nil
}

// newRequestWithNoAuth creates a bare HTTP request with no auth headers.
func newRequestWithNoAuth() *http.Request {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)

	return req
}

// fakeAttributes implements authorizer.Attributes for testing.
type fakeAttributes struct {
	path       string
	user       user.Info
	isResource bool
	resource   string
	verb       string
	namespace  string
}

func (a *fakeAttributes) GetUser() user.Info            { return a.user }
func (a *fakeAttributes) GetVerb() string                { return a.verb }
func (a *fakeAttributes) IsReadOnly() bool {
	return a.verb == "get" || a.verb == "list" || a.verb == "watch"
}

func (a *fakeAttributes) GetNamespace() string          { return a.namespace }
func (a *fakeAttributes) GetResource() string            { return a.resource }
func (a *fakeAttributes) GetSubresource() string        { return "" }
func (a *fakeAttributes) GetName() string                { return "" }
func (a *fakeAttributes) GetAPIGroup() string             { return "" }
func (a *fakeAttributes) GetAPIVersion() string          { return "" }
func (a *fakeAttributes) IsResourceRequest() bool         { return a.isResource }
func (a *fakeAttributes) GetPath() string                 { return a.path }
func (a *fakeAttributes) GetHTTPRequest() *http.Request   { return nil }
func (a *fakeAttributes) GetFieldSelector() (fields.Requirements, error) { return nil, nil }
func (a *fakeAttributes) GetLabelSelector() (labels.Requirements, error)  { return nil, nil }

// Ensure the interfaces are satisfied.
var (
	_ authenticator.Request = (*fakeAuthenticator)(nil)
	_ authorizer.Attributes = (*fakeAttributes)(nil)
)
