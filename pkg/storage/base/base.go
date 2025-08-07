package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
)

var (
	ErrNotFound                = errors.New("resource not found")
	ErrNamespaceNotFound       = errors.New("namespace not found in request context")
	ErrResourceExists          = errors.New("resource already exists")
	ErrRuntimeObjectConversion = errors.New("value cannot be converted to runtime.Object")
)

type StorageStore interface {
	Exists(ctx context.Context, ref types.NamespacedName) (bool, error)
	Read(ctx context.Context, ref types.NamespacedName) ([]byte, error)
	Write(ctx context.Context, ref types.NamespacedName, data []byte) error
	Delete(ctx context.Context, ref types.NamespacedName) error
	List(ctx context.Context) ([][]byte, error)
	ListWithKeys(ctx context.Context) (map[string][]byte, error)
}

var (
	_ rest.StandardStorage   = &storageREST{}
	_ rest.Scoper            = &storageREST{}
	_ rest.Storage           = &storageREST{}
	_ rest.Getter            = &storageREST{}
	_ rest.Updater           = &storageREST{}
	_ rest.Lister            = &storageREST{}
	_ rest.CollectionDeleter = &storageREST{}
	_ rest.GracefulDeleter   = &storageREST{}
	_ rest.Watcher           = &storageREST{}
	_ rest.TableConvertor    = &storageREST{}
	//nolint:misspell
	_ rest.Creater = &storageREST{}
)

// NewStorageREST instantiates a new REST storage for generic resources using an abstracted store.
func NewStorageREST(
	groupResource schema.GroupResource,
	codec runtime.Codec,
	isNamespaced bool,
	newFunc func() runtime.Object,
	newListFunc func() runtime.Object,
	store StorageStore,
) rest.Storage {
	rest := &storageREST{
		TableConvertor: rest.NewDefaultTableConvertor(groupResource),
		codec:          codec,
		isNamespaced:   isNamespaced,
		newFunc:        newFunc,
		newListFunc:    newListFunc,
		store:          store,
		watchers:       make(map[int]*storageWatch),
	}
	return rest
}

type storageREST struct {
	rest.TableConvertor

	codec        runtime.Codec
	isNamespaced bool

	store StorageStore

	muWatchers sync.RWMutex
	watchers   map[int]*storageWatch

	newFunc     func() runtime.Object
	newListFunc func() runtime.Object
}

func (s *storageREST) New() runtime.Object {
	return s.newFunc()
}

func (s *storageREST) Destroy() {}

func (s *storageREST) NewList() runtime.Object {
	return s.newListFunc()
}

func (s *storageREST) NamespaceScoped() bool {
	return s.isNamespaced
}

func (s *storageREST) Get(
	ctx context.Context,
	name string,
	_ *metav1.GetOptions,
) (runtime.Object, error) {
	key, err := s.objectKey(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve object key: %w", err)
	}

	obj, err := s.read(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get JSON BLOB: %w", err)
	}
	return obj, nil
}

func (s *storageREST) List(
	ctx context.Context,
	_ *metainternalversion.ListOptions,
) (runtime.Object, error) {
	dataList, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}

	newListObj := s.NewList()

	value, err := getListPtr(newListObj)
	if err != nil {
		return nil, fmt.Errorf("failed to get list pointer: %w", err)
	}

	for _, data := range dataList {
		newObj := s.newFunc()

		obj, _, err := s.codec.Decode(data, nil, newObj)
		if err != nil {
			return nil, err
		}

		appendItem(value, obj)
	}
	return newListObj, nil
}

func (s *storageREST) Create(
	ctx context.Context,
	obj runtime.Object,
	createValidation rest.ValidateObjectFunc,
	_ *metav1.CreateOptions,
) (runtime.Object, error) {
	if createValidation != nil {
		if err := createValidation(ctx, obj); err != nil {
			return nil, fmt.Errorf("failed to validate object: %w", err)
		}
	}

	accessor, err := meta.Accessor(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to get object accessor: %w", err)
	}

	key, err := s.objectKey(ctx, accessor.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve object key: %w", err)
	}

	exists, err := s.store.Exists(ctx, key)
	if err != nil {
		return nil, err
	}

	if exists {
		return nil, ErrResourceExists
	}

	buf := &bytes.Buffer{}
	if err := s.codec.Encode(obj, buf); err != nil {
		return nil, fmt.Errorf("failed to encode object: %w", err)
	}
	if err := s.store.Write(ctx, key, buf.Bytes()); err != nil {
		return nil, fmt.Errorf("failed to write JSON BLOB: %w", err)
	}

	s.notifyWatchers(watch.Event{
		Type:   watch.Added,
		Object: obj,
	})
	return obj, nil
}

func (s *storageREST) Update(
	ctx context.Context,
	name string,
	objInfo rest.UpdatedObjectInfo,
	createValidation rest.ValidateObjectFunc,
	updateValidation rest.ValidateObjectUpdateFunc,
	forceAllowCreate bool,
	_ *metav1.UpdateOptions,
) (runtime.Object, bool, error) {
	var isCreate bool

	key, err := s.objectKey(ctx, name)
	if err != nil {
		return nil, false, fmt.Errorf("failed to resolve object key: %w", err)
	}

	oldObj, err := s.read(ctx, key)
	if err != nil {
		if !forceAllowCreate {
			return nil, false, fmt.Errorf("failed to get existing object: %w", err)
		}

		isCreate = true
	}

	updatedObj, err := objInfo.UpdatedObject(ctx, oldObj)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get updated object: %w", err)
	}

	if isCreate {
		if createValidation != nil {
			if err := createValidation(ctx, updatedObj); err != nil {
				return nil, false, fmt.Errorf("failed to validate object before creation: %w", err)
			}
		}

		buf := &bytes.Buffer{}
		if err := s.codec.Encode(updatedObj, buf); err != nil {
			return nil, false, fmt.Errorf("failed to encode object: %w", err)
		}
		if err := s.store.Write(ctx, key, buf.Bytes()); err != nil {
			return nil, false, fmt.Errorf("failed to write JSON BLOB: %w", err)
		}

		s.notifyWatchers(watch.Event{
			Type:   watch.Added,
			Object: updatedObj,
		})
		return updatedObj, true, nil
	}

	if updateValidation != nil {
		if err := updateValidation(ctx, updatedObj, oldObj); err != nil {
			return nil, false, fmt.Errorf("failed to validate object before update: %w", err)
		}
	}

	buf := &bytes.Buffer{}
	if err := s.codec.Encode(updatedObj, buf); err != nil {
		return nil, false, fmt.Errorf("failed to encode object: %w", err)
	}
	if err := s.store.Write(ctx, key, buf.Bytes()); err != nil {
		return nil, false, fmt.Errorf("failed to write JSON BLOB: %w", err)
	}

	s.notifyWatchers(watch.Event{
		Type:   watch.Modified,
		Object: updatedObj,
	})
	return updatedObj, false, nil
}

func (s *storageREST) Delete(
	ctx context.Context,
	name string,
	deleteValidation rest.ValidateObjectFunc,
	_ *metav1.DeleteOptions,
) (runtime.Object, bool, error) {
	key, err := s.objectKey(ctx, name)
	if err != nil {
		return nil, false, fmt.Errorf("failed to resolve object key: %w", err)
	}

	exists, err := s.store.Exists(ctx, key)
	if err != nil {
		return nil, false, err
	}

	if !exists {
		return nil, false, ErrNotFound
	}

	oldObj, err := s.read(ctx, key)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read existing object: %w", err)
	}

	if deleteValidation != nil {
		if err := deleteValidation(ctx, oldObj); err != nil {
			return nil, false, fmt.Errorf("failed to validate object before deletion: %w", err)
		}
	}
	if err := s.store.Delete(ctx, key); err != nil {
		return nil, false, fmt.Errorf("failed to delete JSON BLOB: %w", err)
	}

	s.notifyWatchers(watch.Event{
		Type:   watch.Deleted,
		Object: oldObj,
	})
	return oldObj, true, nil
}

func (s *storageREST) DeleteCollection(
	ctx context.Context,
	deleteValidation rest.ValidateObjectFunc,
	_ *metav1.DeleteOptions,
	_ *metainternalversion.ListOptions,
) (runtime.Object, error) {
	dataMap, err := s.store.ListWithKeys(ctx)
	if err != nil {
		return nil, err
	}

	newListObj := s.NewList()

	value, err := getListPtr(newListObj)
	if err != nil {
		return nil, fmt.Errorf("failed to get list pointer: %w", err)
	}

	for key, data := range dataMap {
		newObj := s.newFunc()

		obj, _, err := s.codec.Decode(data, nil, newObj)
		if err != nil {
			return nil, err
		}

		if deleteValidation != nil {
			if err := deleteValidation(ctx, obj); err != nil {
				return nil, fmt.Errorf("failed to validate object before deletion: %w", err)
			}
		}

		key, err := s.objectKey(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve object key: %w", err)
		}

		if err := s.store.Delete(ctx, key); err != nil {
			return nil, fmt.Errorf("failed to delete JSON BLOB: %w", err)
		}

		s.notifyWatchers(watch.Event{
			Type:   watch.Deleted,
			Object: obj,
		})
		appendItem(value, obj)
	}
	return newListObj, nil
}

func (s *storageREST) Watch(
	ctx context.Context,
	options *metainternalversion.ListOptions,
) (watch.Interface, error) {
	watcher := newStorageWatch(s, len(s.watchers))

	list, err := s.List(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("failed to list objects for watch: %w", err)
	}

	danger := reflect.ValueOf(list).Elem()

	items := danger.FieldByName("Items")
	for i := range items.Len() {
		value := items.Index(i).Addr().Interface()

		obj, ok := value.(runtime.Object)
		if !ok {
			return nil, fmt.Errorf("%w: %T", ErrRuntimeObjectConversion, value)
		}

		watcher.ch <- watch.Event{
			Type:   watch.Added,
			Object: obj,
		}
	}

	s.muWatchers.Lock()
	s.watchers[watcher.id] = watcher
	s.muWatchers.Unlock()
	return watcher, nil
}

func (s *storageREST) notifyWatchers(event watch.Event) {
	s.muWatchers.RLock()
	defer s.muWatchers.RUnlock()

	for _, watcher := range s.watchers {
		watcher.ch <- event
	}
}

func (s *storageREST) objectKey(ctx context.Context, name string) (types.NamespacedName, error) {
	if !s.isNamespaced {
		return types.NamespacedName{Name: name}, nil
	}

	ns, exists := genericapirequest.NamespaceFrom(ctx)
	if !exists {
		return types.NamespacedName{}, ErrNamespaceNotFound
	}
	return types.NamespacedName{Name: name, Namespace: ns}, nil
}

func (s *storageREST) read(ctx context.Context, key types.NamespacedName) (runtime.Object, error) {
	data, err := s.store.Read(ctx, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	newObj := s.newFunc()

	decodedObj, _, err := s.codec.Decode(data, nil, newObj)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JSON BLOB: %w", err)
	}
	return decodedObj, nil
}

func getListPtr(listObj runtime.Object) (reflect.Value, error) {
	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("failed to get items pointer: %w", err)
	}

	v, err := conversion.EnforcePtr(listPtr)
	if err != nil || v.Kind() != reflect.Slice {
		return reflect.Value{}, fmt.Errorf("expected a slice pointer but got %T: %w", listPtr, err)
	}
	return v, nil
}

// appendItem appends an item to the list.
func appendItem(value reflect.Value, obj runtime.Object) {
	value.Set(reflect.Append(value, reflect.ValueOf(obj).Elem()))
}
