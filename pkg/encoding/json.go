// Package encoding provides a custom JSON encoder for Kubernetes
// API objects, based on the implementation used in kubectl.
package encoding

import (
	"errors"
	"fmt"
	"io"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/kubectl/pkg/scheme"
)

var (
	// ErrEncodingFailed is an error that indicates the encoding failed.
	ErrEncodingFailed = errors.New("encoding failed")
)

// KubeJSONEncoder is a custom JSON encoder.
type KubeJSONEncoder struct {
	writer io.Writer
}

// NewKubeJSONEncoder creates a new JSON encoder.
func NewKubeJSONEncoder(writer io.Writer) *KubeJSONEncoder {
	return &KubeJSONEncoder{
		writer: writer,
	}
}

// Encode encodes JSON for a Kubernetes API object.
func (e *KubeJSONEncoder) Encode(obj runtime.Object) error {
	bytes, err := e.EncodeWithScheme(obj, scheme.Scheme)
	if err != nil {
		return fmt.Errorf("failed to encode object: %w", err)
	}

	if _, err := e.writer.Write(bytes); err != nil {
		return fmt.Errorf("failed to write object: %w", err)
	}

	return nil
}

// EncodeWithScheme encodes JSON for a Kubernetes API object with a custom scheme.
func (e *KubeJSONEncoder) EncodeWithScheme(obj runtime.Object, customScheme *runtime.Scheme) ([]byte, error) {
	printer, err := genericclioptions.
		NewPrintFlags("render").
		WithTypeSetter(customScheme).
		WithDefaultOutput("json").
		ToPrinter()
	if err != nil {
		return nil, fmt.Errorf("failed to create printer: %w", err)
	}

	if err := printer.PrintObj(obj, e.writer); err != nil {
		return nil, fmt.Errorf("failed to print object: %w", err)
	}

	return nil, nil
}
