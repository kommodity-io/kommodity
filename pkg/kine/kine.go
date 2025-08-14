package kine

import (
	"os"

	corev1 "k8s.io/api/core/v1"

	"github.com/jmoiron/sqlx"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	// "github.com/joho/godotenv"
)

type KindStorage struct {
	config storagebackend.Config
}

func NewKineLegacyStorageConfig(database *sqlx.DB, codecs serializer.CodecFactory) (*storagebackend.Config, error) {
	return NewKineStorageConfig(database, codecs.LegacyCodec(corev1.SchemeGroupVersion))
}

func NewKineStorageConfig(database *sqlx.DB, codec runtime.Codec) (*storagebackend.Config, error) {
	// Can't load dbURI from database object
	kineURI := os.Getenv("KOMMODITY_KINE_URI")
	if kineURI == "" {
		return nil, ErrKommodityKineEnvVarNotSet
	}

	return &storagebackend.Config{
		Type:   storagebackend.StorageTypeETCD3,
		Prefix: "/registry",
		Codec:  codec,
		Transport: storagebackend.TransportConfig{
			// Kine endpoint for Postgres
			// Example: "postgres://user:password@host:5432/dbname?sslmode=disable"
			ServerList: []string{kineURI},
		},
	}, nil
}
