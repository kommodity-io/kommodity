package kine

import (
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage/storagebackend"
)

// NewKineStorageConfig creates the storage configurations to connect to Kine.
func NewKineStorageConfig(codec runtime.Codec) (*storagebackend.Config, error) {
	kineURI := os.Getenv("KOMMODITY_KINE_URI")
	if kineURI == "" {
		return nil, ErrKommodityKineEnvVarNotSet
	}

	return &storagebackend.Config{
		Type:   storagebackend.StorageTypeETCD3,
		Prefix: "/registry",
		Codec:  codec,
		Transport: storagebackend.TransportConfig{
			ServerList: []string{kineURI},
		},
	}, nil
}
