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
	// ErrDecodingFailed is an error that indicates the decoding failed.
	ErrDecodingFailed = errors.New("decoding failed")
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
	return e.EncodeWithScheme(obj, scheme.Scheme)
}

// EncodeWithScheme encodes JSON for a Kubernetes API object with a custom scheme.
func (e *KubeJSONEncoder) EncodeWithScheme(obj runtime.Object, customScheme *runtime.Scheme) error {
	printer, err := genericclioptions.
		NewPrintFlags("render").
		WithTypeSetter(customScheme).
		WithDefaultOutput("json").
		ToPrinter()
	if err != nil {
		return fmt.Errorf("failed to create printer: %w", err)
	}

	if err := printer.PrintObj(obj, e.writer); err != nil {
		return fmt.Errorf("failed to print object: %w", err)
	}

	return nil
}
