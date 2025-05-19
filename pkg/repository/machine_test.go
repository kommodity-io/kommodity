package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	machinev1 "github.com/kommodity-io/kommodity/pkg/apis/core/v1beta1"
)

func TestMachine_New(t *testing.T) {
	machine := NewMachine()
	obj := machine.New()

	assert.IsType(t, &machinev1.Machine{}, obj)
}

func TestMachine_Create(t *testing.T) {
	machine := NewMachine()
	input := &machinev1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-machine",
		},
		Spec: machinev1.MachineSpec{
			Test: "test-value",
		},
	}

	obj, err := machine.Create(context.Background(), input, nil, &metav1.CreateOptions{})
	require.NoError(t, err)

	output, ok := obj.(*machinev1.Machine)
	require.True(t, ok)
	assert.Equal(t, input.Name, output.Name)
	assert.Equal(t, input.Spec.Test, output.Spec.Test)
}

func TestMachine_Get(t *testing.T) {
	machine := NewMachine()

	// First create a machine
	input := &machinev1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-machine",
		},
		Spec: machinev1.MachineSpec{
			Test: "test-value",
		},
	}
	_, err := machine.Create(context.Background(), input, nil, &metav1.CreateOptions{})
	require.NoError(t, err)

	// Then try to get it
	obj, err := machine.Get(context.Background(), "test-machine", &metav1.GetOptions{})
	require.NoError(t, err)

	output, ok := obj.(*machinev1.Machine)
	require.True(t, ok)
	assert.Equal(t, "test-machine", output.Name)
	assert.Equal(t, "test-value", output.Spec.Test)
}

func TestMachine_List(t *testing.T) {
	machine := NewMachine()

	// First create a machine
	input := &machinev1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-machine",
		},
		Spec: machinev1.MachineSpec{
			Test: "test-value",
		},
	}
	_, err := machine.Create(context.Background(), input, nil, &metav1.CreateOptions{})
	require.NoError(t, err)

	// Then try to list it
	obj, err := machine.List(context.Background(), &metav1.ListOptions{})
	require.NoError(t, err)

	output, ok := obj.(*machinev1.MachineList)
	require.True(t, ok)
	assert.Len(t, output.Items, 1)
	assert.Equal(t, "test-machine", output.Items[0].Name)
	assert.Equal(t, "test-value", output.Items[0].Spec.Test)
}

func TestMachine_HTTPHandlers(t *testing.T) {
	mux := http.NewServeMux()
	factory := NewHTTPMuxFactory()
	err := factory(mux)
	require.NoError(t, err)

	t.Run("List", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/core/v1beta1/machines", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var list machinev1.MachineList
		err := json.NewDecoder(w.Body).Decode(&list)
		require.NoError(t, err)
		assert.Empty(t, list.Items)
	})

	t.Run("Create", func(t *testing.T) {
		input := &machinev1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-machine",
			},
			Spec: machinev1.MachineSpec{
				Test: "test-value",
			},
		}

		body, err := json.Marshal(input)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/core/v1beta1/machines", bytes.NewReader(body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var output machinev1.Machine
		err = json.NewDecoder(w.Body).Decode(&output)
		require.NoError(t, err)
		assert.Equal(t, input.Name, output.Name)
		assert.Equal(t, input.Spec.Test, output.Spec.Test)
	})

	t.Run("Get", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/core/v1beta1/machines/test-machine", nil)
		req.SetPathValue("name", "test-machine")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var output machinev1.Machine
		err := json.NewDecoder(w.Body).Decode(&output)
		require.NoError(t, err)
		assert.Equal(t, "test-machine", output.Name)
		assert.Equal(t, "test-value", output.Spec.Test)
	})

	t.Run("GetMissingName", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/core/v1beta1/machines/", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("MethodNotAllowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/core/v1beta1/machines", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}
