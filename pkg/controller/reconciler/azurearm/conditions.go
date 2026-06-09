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

// setNotFound marks the resource as not found in Azure. This is used in the
// skip-policy read-only path when the ARM GET returns 404. CAPZ's adoption
// semantics key on AzureResourceNotFound + skip to decide whether to adopt a
// pre-existing resource.
func setNotFound(obj genruntime.ARMMetaObject) {
	conditions.SetCondition(obj, readyConditionBuilder().ReadyCondition(
		conditions.ConditionSeverityInfo,
		obj.GetGeneration(),
		"AzureResourceNotFound",
		"the Azure resource was not found",
	))
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
