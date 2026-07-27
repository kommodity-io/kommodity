package libkapi

import (
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/request/anonymous"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
)

// defaultAuthenticator treats every request as anonymous. There is no
// authentication in this version of libkapi: see doc.go and the package's
// security-posture warning.
func defaultAuthenticator() authenticator.Request {
	return anonymous.NewAuthenticator(nil)
}

// defaultAuthorizer allows every request. There is no authorization in this
// version of libkapi: see doc.go and the package's security-posture warning.
func defaultAuthorizer() authorizer.Authorizer {
	return authorizerfactory.NewAlwaysAllowAuthorizer()
}
