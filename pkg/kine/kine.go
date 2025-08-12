package kine

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/jmoiron/sqlx"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/storage/storagebackend"
)

type KindStorage struct {
	config storagebackend.Config
}

func NewKineLegacyStorageConfig(database *sqlx.DB, codecs serializer.CodecFactory) storagebackend.Config {
	return NewKineStorageConfig(database, codecs.LegacyCodec(corev1.SchemeGroupVersion))
}

func NewKineStorageConfig(database *sqlx.DB, codec runtime.Codec) storagebackend.Config {
	return storagebackend.Config{
		Type:   storagebackend.StorageTypeETCD3,
		Prefix: "/registry",
		Codec:  codec,
		Transport: storagebackend.TransportConfig{
			// Kine endpoint for Postgres
			// Example: "postgres://user:password@host:5432/dbname?sslmode=disable"
			ServerList: []string{"postgres://kommodity:kommodity@localhost:5432/kommodity?sslmode=disable"},
		},
	}
}
