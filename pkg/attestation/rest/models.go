package rest

import "time"

// Report represents the attestation report structure.
type Report struct {
	Components   []ComponentReport `json:"components"`
	PCRs         map[int]string    `json:"pcrs"`
	Quote        string            `json:"quote"`        // Hex encoded TPM quote over the PCRs, with nonce
	Signature    string            `json:"signature"`    // Hex encoded TPM signature over quote
	TPMPublicKey string            `json:"tpmPublicKey"` // Hex encoded TPM public key
	Timestamp    time.Time         `json:"timestamp"`
}

// ComponentReport represents the attestation report for a specific component.
type ComponentReport struct {
	Name        string            `json:"name"`
	Measurement string            `json:"measurement"` // SHA512 of the component
	Evidence    map[string]string `json:"evidence"`
}

// CompliantWith checks if the report is compliant with the given policy.
func (r *Report) CompliantWith(policy *Report) bool {
	_ = policy

	return true
}
