package memory

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
	"k8s.io/apimachinery/pkg/watch"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
)

var (
	// ErrNotFound is returned when the requested JSON BLOB is not found.
	ErrNotFound = errors.New("resource not found")
	// ErrNamespaceNotFound is returned when the namespace is not found in the context.
	ErrNamespaceNotFound = errors.New("namespace not found in request context")
	// ErrResourceExists is returned when trying to create an object that already exists.
	ErrResourceExists = errors.New("resource already exists")
)

// Ensure that the necessary interfaces are implemented.
var (
	_ rest.StandardStorage = &jsonblobREST{}
	_ rest.Scoper          = &jsonblobREST{}
	_ rest.Storage         = &jsonblobREST{}
)

// NewJSONBLOBREST instantiates a new REST storage for JSON BLOBs.
func NewJSONBLOBREST(
	groupResource schema.GroupResource,
	codec runtime.Codec,
	isNamespaced bool,
	newFunc func() runtime.Object,
	newListFunc func() runtime.Object,
) rest.Storage {

	rest := &jsonblobREST{
		TableConvertor: rest.NewDefaultTableConvertor(groupResource),
		codec:          codec,
		isNamespaced:   isNamespaced,
		newFunc:        newFunc,
		newListFunc:    newListFunc,
		watchers:       make(map[int]*jsonWatch, 10),
	}

	return rest
}

// jsonblobREST implements the REST storage interface for JSON BLOBs.
type jsonblobREST struct {
	rest.TableConvertor
	codec        runtime.Codec
	isNamespaced bool

	muWatchers sync.RWMutex
	watchers   map[int]*jsonWatch

	newFunc     func() runtime.Object
	newListFunc func() runtime.Object

	muStore sync.RWMutex
	store   map[string]string
}

// New returns a new instance of the object.
func (j *jsonblobREST) New() runtime.Object {
	return j.newFunc()
}

// Destroy performs cleanup operations for the REST storage.
func (j *jsonblobREST) Destroy() {
	// Should we clean up watchers here?
}

// NewList returns a new list instance of the object.
func (j *jsonblobREST) NewList() runtime.Object {
	return j.newListFunc()
}

// NamespaceScoped indicates whether the resource is namespaced.
func (j *jsonblobREST) NamespaceScoped() bool {
	return j.isNamespaced
}

// Get returns the object for the given name.
func (j *jsonblobREST) Get(
	ctx context.Context,
	name string,
	options *metav1.GetOptions,
) (runtime.Object, error) {
	j.muStore.RLock()
	defer j.muStore.RUnlock()

	key, err := j.objectKey(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve object key: %w", err)
	}

	obj, err := j.read(key)
	if err != nil {
		return nil, fmt.Errorf("failed to get JSON BLOB: %w", err)
	}

	return obj, nil
}

// List returns a list of objects.
func (j *jsonblobREST) List(
	ctx context.Context,
	options *metainternalversion.ListOptions,
) (runtime.Object, error) {
	j.muStore.RLock()
	defer j.muStore.RUnlock()

	newListObj := j.NewList()

	value, err := getListPtr(newListObj)
	if err != nil {
		return nil, fmt.Errorf("failed to get list pointer: %w", err)
	}

	for key, _ := range j.store {
		obj, err := j.read(key)
		if err != nil {
			return nil, fmt.Errorf("failed to read JSON BLOB for key %s: %w", key, err)
		}

		appendItem(value, obj)
	}

	return newListObj, nil
}

// Create creates a new object in the store.
func (j *jsonblobREST) Create(
	ctx context.Context,
	obj runtime.Object,
	createValidation rest.ValidateObjectFunc,
	options *metav1.CreateOptions,
) (runtime.Object, error) {
	j.muStore.Lock()
	defer j.muStore.Unlock()

	if createValidation != nil {
		if err := createValidation(ctx, obj); err != nil {
			return nil, fmt.Errorf("failed to validate object: %w", err)
		}
	}

	accessor, err := meta.Accessor(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to get object accessor: %w", err)
	}

	key, err := j.objectKey(ctx, accessor.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve object key: %w", err)
	}

	if j.exists(key) {
		return nil, ErrResourceExists
	}

	if err := j.write(key, obj); err != nil {
		return nil, fmt.Errorf("failed to write JSON BLOB: %w", err)
	}

	j.notifyWatchers(watch.Event{
		Type:   watch.Added,
		Object: obj,
	})

	return obj, nil
}

// Update updates an existing object in the store.
func (j *jsonblobREST) Update(
	ctx context.Context,
	name string,
	objInfo rest.UpdatedObjectInfo,
	createValidation rest.ValidateObjectFunc,
	updateValidation rest.ValidateObjectUpdateFunc,
	forceAllowCreate bool,
	options *metav1.UpdateOptions,
) (runtime.Object, bool, error) {
	j.muStore.Lock()
	defer j.muStore.Unlock()

	var isCreate bool

	key, err := j.objectKey(ctx, name)
	if err != nil {
		return nil, false, fmt.Errorf("failed to resolve object key: %w", err)
	}

	oldObj, err := j.read(key)
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

		if err := j.write(key, updatedObj); err != nil {
			return nil, false, fmt.Errorf("failed to write JSON BLOB: %w", err)
		}

		j.notifyWatchers(watch.Event{
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

	if err := j.write(key, updatedObj); err != nil {
		return nil, false, fmt.Errorf("failed to write JSON BLOB: %w", err)
	}

	j.notifyWatchers(watch.Event{
		Type:   watch.Modified,
		Object: updatedObj,
	})

	return updatedObj, false, nil
}

// Delete removes an object from the store.
func (j *jsonblobREST) Delete(
	ctx context.Context,
	name string,
	deleteValidation rest.ValidateObjectFunc,
	options *metav1.DeleteOptions,
) (runtime.Object, bool, error) {
	j.muStore.Lock()
	defer j.muStore.Unlock()

	key, err := j.objectKey(ctx, name)
	if err != nil {
		return nil, false, fmt.Errorf("failed to resolve object key: %w", err)
	}

	if !j.exists(key) {
		return nil, false, ErrNotFound
	}

	oldObj, err := j.read(key)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read existing object: %w", err)
	}

	if deleteValidation != nil {
		if err := deleteValidation(ctx, oldObj); err != nil {
			return nil, false, fmt.Errorf("failed to validate object before deletion: %w", err)
		}
	}

	if err := j.delete(key); err != nil {
		return nil, false, fmt.Errorf("failed to delete JSON BLOB: %w", err)
	}

	j.notifyWatchers(watch.Event{
		Type:   watch.Deleted,
		Object: oldObj,
	})

	return oldObj, true, nil
}

// DeleteCollection deletes a collection of objects.
func (j *jsonblobREST) DeleteCollection(
	ctx context.Context,
	deleteValidation rest.ValidateObjectFunc,
	options *metav1.DeleteOptions,
	listOptions *metainternalversion.ListOptions,
) (runtime.Object, error) {
	j.muStore.Lock()
	defer j.muStore.Unlock()

	newListObj := j.NewList()

	value, err := getListPtr(newListObj)
	if err != nil {
		return nil, fmt.Errorf("failed to get list pointer: %w", err)
	}

	for key, _ := range j.store {
		if !j.exists(key) {
			return nil, ErrNotFound
		}

		oldObj, err := j.read(key)
		if err != nil {
			return nil, fmt.Errorf("failed to get existing object: %w", err)
		}

		if deleteValidation != nil {
			if err := deleteValidation(ctx, oldObj); err != nil {
				return nil, fmt.Errorf("failed to validate object before deletion: %w", err)
			}
		}

		if err := j.delete(key); err != nil {
			return nil, fmt.Errorf("failed to delete JSON BLOB: %w", err)
		}

		j.notifyWatchers(watch.Event{
			Type:   watch.Deleted,
			Object: oldObj,
		})

		appendItem(value, oldObj)
	}

	return newListObj, nil
}

// Watch returns a watch.Interface for the given options.
func (j *jsonblobREST) Watch(
	ctx context.Context,
	options *metainternalversion.ListOptions,
) (watch.Interface, error) {
	watcher := newJSONWatch(j, len(j.watchers))

	// On initial watch, send all the existing objects.
	list, err := j.List(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("failed to list objects for watch: %w", err)
	}

	// This seems nasty, but I don't know how to
	// implement this more nicely at the moment.
	danger := reflect.ValueOf(list).Elem()
	items := danger.FieldByName("Items")

	for i := range items.Len() {
		obj := items.Index(i).Addr().Interface().(runtime.Object)
		watcher.ch <- watch.Event{
			Type:   watch.Added,
			Object: obj,
		}
	}

	// Register the watcher.
	j.muWatchers.Lock()
	j.watchers[watcher.id] = watcher
	j.muWatchers.Unlock()

	return watcher, nil
}

// notifyWatchers notifies all watchers about an event.
func (j *jsonblobREST) notifyWatchers(event watch.Event) {
	j.muWatchers.RLock()
	defer j.muWatchers.RUnlock()

	for _, watcher := range j.watchers {
		watcher.ch <- event
	}
}

// objectKey constructs a key for the JSON BLOB in the store.
func (j *jsonblobREST) objectKey(ctx context.Context, name string) (string, error) {
	if !j.isNamespaced {
		return name, nil
	}

	ns, exists := genericapirequest.NamespaceFrom(ctx)
	if !exists {
		return "", ErrNamespaceNotFound
	}

	return ns + "/" + name, nil
}

// exists checks if the object with the given key exists in the store.
// This method is not thread-safe. Make sure to call it with the store's
// mutex locked.
func (j *jsonblobREST) exists(key string) bool {
	_, exists := j.store[key]

	return exists
}

// read reads the JSON BLOB from the store.
func (j *jsonblobREST) read(key string) (runtime.Object, error) {
	data, exists := j.store[key]
	if !exists {
		return nil, ErrNotFound
	}

	newObj := j.newFunc()
	decodedObj, _, err := j.codec.Decode([]byte(data), nil, newObj)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JSON BLOB: %w", err)
	}

	return decodedObj, nil
}

// write writes the object to the store.
func (j *jsonblobREST) write(key string, obj runtime.Object) error {
	buf := &bytes.Buffer{}

	err := j.codec.Encode(obj, buf)
	if err != nil {
		return fmt.Errorf("failed to encode object: %w", err)
	}

	j.store[key] = buf.String()

	return nil
}

// delete removes the object with the given key from the store.
func (j *jsonblobREST) delete(key string) error {
	if _, exists := j.store[key]; !exists {
		return ErrNotFound
	}

	delete(j.store, key)

	return nil
}

// getListPtr retrieves the pointer to the items slice in the list object.
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
