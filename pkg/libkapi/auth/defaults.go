package auth

import (
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/request/anonymous"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
)

// DefaultAuthenticator treats every request as anonymous. It is what a
// server ends up with when no authentication Option is passed: see doc.go
// and the package's security-posture warning.
func DefaultAuthenticator() authenticator.Request {
	return anonymous.NewAuthenticator(nil)
}

// DefaultAuthorizer allows every request. It is what a server ends up with
// when no authorization Option is passed: see doc.go and the package's
// security-posture warning.
func DefaultAuthorizer() authorizer.Authorizer {
	return authorizerfactory.NewAlwaysAllowAuthorizer()
}
