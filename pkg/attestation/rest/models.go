package rest

import "time"

// Report represents the attestation report structure.
type Report struct {
	Components []ComponentReport `json:"components"`
	Timestamp  time.Time         `json:"timestamp"`
}

// ComponentReport represents the attestation report for a specific component.
type ComponentReport struct {
	Name        string            `json:"name"`
	PCRs        map[int]string    `json:"pcrs"`
	Measurement string            `json:"measurement"` // SHA512 of the component
	Quote       string            `json:"quote"`       // Hex encoded TPM quote (includes nonce)
	Signature   string            `json:"signature"`   // Hex encoded TPM signature over quote
	Evidence    map[string]string `json:"evidence"`
}

// CompliantWith checks if the report is compliant with the given policy.
func (r *Report) CompliantWith(policy *Report) bool {
	if len(r.Components) != len(policy.Components) {
		return false
	}

	// Build a map for fast lookup by Name in machine report
	machineComponents := make(map[string]ComponentReport)
	for _, comp := range r.Components {
		machineComponents[comp.Name] = comp
	}

	for _, policyComponent := range policy.Components {
		component, ok := machineComponents[policyComponent.Name]
		if !ok {
			return false
		}

		if !compareComponent(component, policyComponent) {
			return false
		}
	}

	return true
}

func compareComponent(machineComponent, policyComponent ComponentReport) bool {
	if len(machineComponent.PCRs) != len(policyComponent.PCRs) {
		return false
	}

	for k, v := range policyComponent.PCRs {
		rv, ok := machineComponent.PCRs[k]
		if !ok || rv != v {
			return false
		}
	}

	if machineComponent.Measurement != policyComponent.Measurement {
		return false
	}

	if machineComponent.Quote != policyComponent.Quote {
		return false
	}

	if machineComponent.Signature != policyComponent.Signature {
		return false
	}

	return true
}
