// Package storage resolves libkapi's polymorphic storage connection string
// into a running backend: an in-process Kine endpoint for the SQL/NATS
// dialects, or a direct etcd3-compatible client for etcd:// and unix://
// schemes. It also builds the RESTOptionsGetter handed to the apiserver's
// registry storage providers.
package storage
