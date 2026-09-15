// Package auth provides pluggable authentication and authorization
// strategies for a libkapi Server.
//
// It implements the functional-options pattern: callers pass Option values
// to libkapi.New, which are applied in order to build the server's
// authenticator (who is the user) and authorizer (what can they do).
//
// If no options are passed, the server defaults to anonymous authentication
// and always-allow authorization — suitable only for trusted networks.
package auth
