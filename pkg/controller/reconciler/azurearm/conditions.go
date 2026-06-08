package azurearm

import (
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/benbjohnson/clock"
)

// readyConditionBuilder constructs a Ready-condition builder backed by the real
// clock. The conditions it produces match the contract CAPZ reads from ASO
// resources (condition type "Ready" with ASO's reason/severity vocabulary).
func readyConditionBuilder() *conditions.ReadyConditionBuilder {
	return conditions.NewReadyConditionBuilder(conditions.NewPositiveConditionBuilder(clock.New()))
}

// setReconciling marks the resource as not-yet-ready while an ARM operation is in
// flight (reason "Reconciling", which CAPZ treats as a long-running operation).
func setReconciling(obj genruntime.ARMMetaObject) {
	conditions.SetCondition(obj, readyConditionBuilder().Reconciling(obj.GetGeneration()))
}

// setDeleting marks the resource as being deleted.
func setDeleting(obj genruntime.ARMMetaObject) {
	conditions.SetCondition(obj, readyConditionBuilder().Deleting(obj.GetGeneration()))
}

// setSucceeded marks the resource Ready=True for the current generation.
func setSucceeded(obj genruntime.ARMMetaObject) {
	conditions.SetCondition(obj, readyConditionBuilder().Succeeded(obj.GetGeneration()))
}

// setFailed marks the resource as permanently failed (severity=Error) due to a
// terminal ARM error. CAPZ treats Error severity as non-retryable; human
// intervention (spec fix or annotation change) is required.
func setFailed(obj genruntime.ARMMetaObject, message string) {
	conditions.SetCondition(obj, readyConditionBuilder().ReadyCondition(
		conditions.ConditionSeverityError,
		obj.GetGeneration(),
		"AzureError",
		message,
	))
}
