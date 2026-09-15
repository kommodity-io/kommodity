//nolint:testpackage // white-box tests exercise unexported reconciler internals
package azurearm

import (
	"testing"

	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetReconcilingSetsNotReady(t *testing.T) {
	t.Parallel()

	rg := newResourceGroup("my-rg")

	setReconciling(rg)

	cond, found := findReadyCondition(rg.GetConditions())
	if !found {
		t.Fatal("expected a Ready condition to be set")
	}

	if cond.Status != metav1.ConditionFalse {
		t.Fatalf("Ready status = %q, want False", cond.Status)
	}

	if cond.Reason != conditions.ReasonReconciling.Name {
		t.Fatalf("Ready reason = %q, want %q", cond.Reason, conditions.ReasonReconciling.Name)
	}
}

func TestSetSucceededSetsReady(t *testing.T) {
	t.Parallel()

	rg := newResourceGroup("my-rg")

	setSucceeded(rg)

	cond, found := findReadyCondition(rg.GetConditions())
	if !found {
		t.Fatal("expected a Ready condition to be set")
	}

	if cond.Status != metav1.ConditionTrue {
		t.Fatalf("Ready status = %q, want True", cond.Status)
	}
}

func TestSetDeletingSetsNotReady(t *testing.T) {
	t.Parallel()

	rg := newResourceGroup("my-rg")

	setDeleting(rg)

	cond, found := findReadyCondition(rg.GetConditions())
	if !found {
		t.Fatal("expected a Ready condition to be set")
	}

	if cond.Reason != conditions.ReasonDeleting.Name {
		t.Fatalf("Ready reason = %q, want %q", cond.Reason, conditions.ReasonDeleting.Name)
	}
}

func findReadyCondition(conds []conditions.Condition) (conditions.Condition, bool) {
	for _, cond := range conds {
		if cond.Type == conditions.ConditionTypeReady {
			return cond, true
		}
	}

	return conditions.Condition{}, false
}
