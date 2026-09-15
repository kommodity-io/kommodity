//nolint:testpackage // white-box tests exercise unexported reconciler internals
package azurearm

import (
	"testing"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
)

// nestedSpec exercises the recursive reference walker with pointers, slices and
// nested structs.
type nestedSpec struct {
	Direct   genruntime.ResourceReference
	Pointer  *genruntime.ResourceReference
	Slice    []innerSpec
	Excluded string
}

type innerSpec struct {
	Reference *genruntime.ResourceReference
}

func TestFindResourceReferences(t *testing.T) {
	t.Parallel()

	armA := "/subscriptions/s/resourceGroups/a"
	armB := "/subscriptions/s/resourceGroups/b"
	armC := "/subscriptions/s/resourceGroups/c"

	spec := &nestedSpec{
		Direct:  genruntime.ResourceReference{ARMID: armA},
		Pointer: &genruntime.ResourceReference{ARMID: armB},
		Slice:   []innerSpec{{Reference: &genruntime.ResourceReference{ARMID: armC}}},
	}

	refs := findResourceReferences(spec)
	if len(refs) != 3 {
		t.Fatalf("found %d references, want 3", len(refs))
	}

	for _, armID := range []string{armA, armB, armC} {
		if _, ok := refs[genruntime.ResourceReference{ARMID: armID}]; !ok {
			t.Fatalf("expected reference %q to be found", armID)
		}
	}
}

func TestBuildResolvedDetailsResourceGroupHasNoReferences(t *testing.T) {
	t.Parallel()

	rg := newResourceGroup("my-rg")

	details, err := buildResolvedDetails(rg)
	if err != nil {
		t.Fatalf("buildResolvedDetails returned error: %v", err)
	}

	if details.Name != "my-rg" {
		t.Fatalf("details.Name = %q, want my-rg", details.Name)
	}
}
