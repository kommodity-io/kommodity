// Package controllers provides post-start hooks for ServiceAccount token
// management: token issuance (the SA token controller) and signing-key
// rotation (watching the signing-key Secret and rotating tokens on change).
//
// Both are started as post-start hooks on the server's GenericAPIServer,
// using the SharedInformerFactory and LoopbackClientConfig from the
// server's RecommendedConfig.
package controllers
