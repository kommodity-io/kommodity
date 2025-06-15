package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apirest "k8s.io/apiserver/pkg/registry/rest"

	machinev1 "github.com/kommodity-io/kommodity/pkg/apis/core/v1beta1"
	"github.com/kommodity-io/kommodity/pkg/encoding"
)

// Machine implements the RESTStorage interface for Machine resources.
type Machine struct {
	// storage is an in-memory map to store machines.
	storage map[string]*machinev1.Machine
	// mu protects the storage map from concurrent access.
	mu sync.RWMutex
	// scheme is the runtime scheme used for type registration.
	scheme *runtime.Scheme
}

// NewMachine creates a new instance of Machine with a properly configured scheme.
func NewMachine() *Machine {
	scheme := runtime.NewScheme()
	schemeGroupVersion := schema.GroupVersion{Group: "core.kommodity.io", Version: "v1beta1"}

	// Register the types with the scheme.
	scheme.AddKnownTypes(schemeGroupVersion,
		&machinev1.Machine{},
		&machinev1.MachineList{},
	)
	metav1.AddToGroupVersion(scheme, schemeGroupVersion)

	return &Machine{
		storage: make(map[string]*machinev1.Machine),
		scheme:  scheme,
	}
}

// New returns a new instance of Machine.
func (r *Machine) New() runtime.Object {
	return &machinev1.Machine{}
}

// Create creates a new Machine.
func (r *Machine) Create(ctx context.Context, obj runtime.Object, createValidation apirest.ValidateObjectFunc, options *metav1.CreateOptions) (runtime.Object, error) {
	machine := obj.(*machinev1.Machine)
	if machine.Name == "" {
		return nil, fmt.Errorf("failed to create machine: name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.storage[machine.Name]; exists {
		return nil, fmt.Errorf("failed to create machine: machine with name %s already exists", machine.Name)
	}

	r.storage[machine.Name] = machine
	return machine, nil
}

// Get retrieves a Machine by name.
func (r *Machine) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	machine, exists := r.storage[name]
	if !exists {
		return nil, fmt.Errorf("failed to get machine: machine with name %s not found", name)
	}

	return machine, nil
}

// List returns a list of Machines.
func (r *Machine) List(ctx context.Context, options *metav1.ListOptions) (runtime.Object, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	machines := make([]machinev1.Machine, 0, len(r.storage))
	for _, machine := range r.storage {
		machines = append(machines, *machine)
	}

	return &machinev1.MachineList{
		Items: machines,
	}, nil
}

// Destroy cleans up resources on shutdown.
func (r *Machine) Destroy() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.storage = nil
}

// NamespaceScoped returns true if the storage is namespaced.
func (r *Machine) NamespaceScoped() bool {
	return true
}

// NewHTTPMuxFactory returns a function that initializes the HTTP mux with the Machine REST storage.
func NewHTTPMuxFactory() func(*http.ServeMux) error {
	return func(mux *http.ServeMux) error {
		// Create a new Machine REST storage
		machineStorage := NewMachine()

		// Register the Machine REST storage with the HTTP mux
		mux.HandleFunc("GET /api/core/v1beta1/machines", func(w http.ResponseWriter, r *http.Request) {
			// Handle list request
			obj, err := machineStorage.List(r.Context(), &metav1.ListOptions{})
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to list machines: %v", err), http.StatusInternalServerError)
				return
			}

			if err := encoding.NewKubeJSONEncoder(w).EncodeWithScheme(obj, machineStorage.scheme); err != nil {
				http.Error(w, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
				return
			}
		})

		mux.HandleFunc("POST /api/core/v1beta1/machines", func(w http.ResponseWriter, r *http.Request) {
			// Handle create request
			var machine machinev1.Machine
			if err := json.NewDecoder(r.Body).Decode(&machine); err != nil {
				http.Error(w, fmt.Sprintf("failed to decode request: %v", err), http.StatusBadRequest)
				return
			}

			obj, err := machineStorage.Create(r.Context(), &machine, nil, &metav1.CreateOptions{})
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to create machine: %v", err), http.StatusInternalServerError)
				return
			}

			if err := encoding.NewKubeJSONEncoder(w).EncodeWithScheme(obj, machineStorage.scheme); err != nil {
				http.Error(w, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
				return
			}
		})

		// Register the Machine REST storage with the HTTP mux for individual resources
		mux.HandleFunc("GET /api/core/v1beta1/machines/{name}", func(w http.ResponseWriter, r *http.Request) {
			// Extract the name from the URL path parameters
			name := r.PathValue("name")
			if name == "" {
				http.Error(w, "missing name parameter", http.StatusBadRequest)
				return
			}

			// Handle get request
			obj, err := machineStorage.Get(r.Context(), name, &metav1.GetOptions{})
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to get machine: %v", err), http.StatusInternalServerError)
				return
			}

			if err := encoding.NewKubeJSONEncoder(w).EncodeWithScheme(obj, machineStorage.scheme); err != nil {
				http.Error(w, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
				return
			}
		})

		return nil
	}
}
