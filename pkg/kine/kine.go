package kine

import (
	"os"

	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/storage/storagebackend"
)

// NewKineLegacyStorageConfig creates the storage configurations to connect to Kine using the codecs for legacy API.
func NewKineLegacyStorageConfig(codecs serializer.CodecFactory) (*storagebackend.Config, error) {
	return NewKineStorageConfig(codecs.LegacyCodec(corev1.SchemeGroupVersion))
}

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
