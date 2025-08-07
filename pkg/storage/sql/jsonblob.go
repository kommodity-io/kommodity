package sql

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	genericregistry "k8s.io/apiserver/pkg/registry/generic"
	restregistry "k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/server/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"sigs.k8s.io/apiserver-runtime/pkg/builder/resource"

	"github.com/jmoiron/sqlx"
	"github.com/kommodity-io/kommodity/pkg/apiserver"
	kstorage "github.com/kommodity-io/kommodity/pkg/storage/base"
)

type JSONDatabaseStore struct {
	db        *sqlx.DB
	tableName string
}

func NewJSONDatabaseStore(db *sqlx.DB, gvr schema.GroupVersionResource) *JSONDatabaseStore {
	return &JSONDatabaseStore{db: db, tableName: constructTableName(gvr)}
}

func (j *JSONDatabaseStore) Migrate() error {
	query := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (
            name TEXT NOT NULL,
            namespace TEXT NOT NULL,
            data JSONB NOT NULL,
            PRIMARY KEY (name, namespace)
        );`, j.tableName)

	_, err := j.db.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to create table %s: %w", j.tableName, err)
	}

	return nil
}

// Delete implements storage.StorageStore.
func (j JSONDatabaseStore) Delete(ctx context.Context, ref types.NamespacedName) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE name = $1 AND namespace = $2", j.tableName)
	_, err := j.db.ExecContext(ctx, query, ref.Name, ref.Namespace)

	return err
}

// Exists implements storage.StorageStore.
func (j JSONDatabaseStore) Exists(ctx context.Context, ref types.NamespacedName) (bool, error) {
	query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE name = $1 AND namespace = $2)", j.tableName)

	var exists bool

	err := j.db.GetContext(ctx, &exists, query, ref.Name, ref.Namespace)

	return exists, err
}

// List implements storage.StorageStore.
func (j JSONDatabaseStore) List(ctx context.Context) ([][]byte, error) {
	query := fmt.Sprintf("SELECT data FROM %s", j.tableName)
	rows, err := j.db.QueryxContext(ctx, query)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var result [][]byte
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}

		result = append(result, data)
	}

	return result, nil
}

// ListWithKeys implements storage.StorageStore.
func (j JSONDatabaseStore) ListWithKeys(ctx context.Context) (map[string][]byte, error) {
	panic("unimplemented")
}

// Read implements storage.StorageStore.
func (j JSONDatabaseStore) Read(ctx context.Context, ref types.NamespacedName) ([]byte, error) {
	query := fmt.Sprintf("SELECT data FROM %s WHERE name = $1 AND namespace = $2", j.tableName)

	var data []byte

	err := j.db.GetContext(ctx, &data, query, ref.Name, ref.Namespace)

	return data, err
}

// Write implements storage.StorageStore.
func (j JSONDatabaseStore) Write(ctx context.Context, ref types.NamespacedName, data []byte) error {
	query := fmt.Sprintf(
		`INSERT INTO %s (name, namespace, data) VALUES ($1, $2, $3)
         ON CONFLICT (name, namespace) DO UPDATE SET data = EXCLUDED.data`, j.tableName)
	_, err := j.db.ExecContext(ctx, query, ref.Name, ref.Namespace, data)

	return err
}

func constructTableName(gvr schema.GroupVersionResource) string {
	// Construct the table name based on the GroupVersionResource
	return strings.ReplaceAll(fmt.Sprintf("%s__%s__%s", gvr.Group, gvr.Version, gvr.Resource), ".", "_")
}

func NewJSONStorageProvider(obj resource.Object, db *sqlx.DB) apiserver.ResourceHandlerProvider {
	return func(scheme *runtime.Scheme, _ genericregistry.RESTOptionsGetter) (restregistry.Storage, error) {
		gvr := obj.GetGroupVersionResource()
		//nolint:varnamelen
		gr := gvr.GroupResource()

		codec, _, err := storage.NewStorageCodec(storage.StorageCodecConfig{
			StorageMediaType:  runtime.ContentTypeJSON,
			StorageSerializer: serializer.NewCodecFactory(scheme),
			StorageVersion:    scheme.PrioritizedVersionsForGroup(gvr.Group)[0],
			MemoryVersion:     scheme.PrioritizedVersionsForGroup(gvr.Group)[0],
			Config:            storagebackend.Config{}, // useless fields..
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create storage codec: %w", err)
		}

		jsonStore := NewJSONDatabaseStore(db, gvr)
		jsonStore.Migrate()

		return kstorage.NewStorageREST(
			gr,
			codec,
			obj.NamespaceScoped(),
			obj.New,
			obj.NewList,
			jsonStore,
		), nil
	}
}
