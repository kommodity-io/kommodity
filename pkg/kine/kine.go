// Package kine provides utilities to configure Kine as the storage backend for Kommodity.
package kine

import (
	"github.com/kommodity-io/kommodity/pkg/config"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage/storagebackend"
)

// NewKineStorageConfig creates the storage configurations to connect to Kine.
func NewKineStorageConfig(cfg *config.KommodityConfig, codec runtime.Codec) (*storagebackend.Config, error) {
	return &storagebackend.Config{
		Type:   storagebackend.StorageTypeETCD3,
		Prefix: "/registry",
		Codec:  codec,
		Transport: storagebackend.TransportConfig{
			ServerList: []string{*cfg.KineURI},
		},
	}, nil
}
