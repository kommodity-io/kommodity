package rest

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"time"

	"github.com/google/go-tpm/tpm2"
)

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
func (r *Report) CompliantWith(nonce string, policy *Report) (bool, error) {
	quoteInfo, quoteDigest, err := parseTPMAttest(r.Quote, nonce)
	if err != nil {
		return false, fmt.Errorf("parse TPM attestation: %w", err)
	}

	pcrDigest, err := computePCRDigest(quoteInfo, r.PCRs)
	if err != nil {
		return false, fmt.Errorf("compute PCR digest: %w", err)
	}

	if !reflect.DeepEqual(pcrDigest, quoteInfo.PCRDigest.Buffer) {
		return false, ErrPCRDigestMismatch
	}

	eccSig, ecdsaPub, err := parseTPMSignature(r.Signature, r.TPMPublicKey)
	if err != nil {
		return false, fmt.Errorf("parse TPM signature: %w", err)
	}

	R := new(big.Int).SetBytes(eccSig.SignatureR.Buffer)
	S := new(big.Int).SetBytes(eccSig.SignatureS.Buffer)

	if !ecdsa.Verify(ecdsaPub, quoteDigest, R, S) {
		return false, ErrTPMSignatureInvalid
	}

	componentsValid, err := validateComponents(r.Components, policy.Components)
	if err != nil {
		return false, fmt.Errorf("validate components: %w", err)
	}

	if !componentsValid {
		return false, nil
	}

	pcrsValid, err := validatePCRs(r.PCRs, policy.PCRs)
	if err != nil {
		return false, fmt.Errorf("validate PCRs: %w", err)
	}

	if !pcrsValid {
		return false, nil
	}

	return true, nil
}

func validateComponents(reportComponents []ComponentReport, policyComponents []ComponentReport) (bool, error) {
	reportMap := make(map[string]ComponentReport)
	for _, comp := range reportComponents {
		reportMap[comp.Name] = comp
	}

	for _, want := range policyComponents {
		got, ok := reportMap[want.Name]
		if !ok {
			return false, fmt.Errorf("%w: %s", ErrMissingComponent, want.Name)
		}

		if got.Measurement != want.Measurement {
			return false, fmt.Errorf("%w: %s", ErrComponentMismatch, want.Name)
		}
	}

	return true, nil
}

func validatePCRs(reportPCRs map[int]string, policyPCRs map[int]string) (bool, error) {
	for idx, want := range policyPCRs {
		got, ok := reportPCRs[idx]
		if !ok || !reflect.DeepEqual(got, want) {
			return false, fmt.Errorf("%w: %d", ErrPCRMismatch, idx)
		}
	}

	return true, nil
}

func parseTPMSignature(hexSignature, hexPublicKey string) (*tpm2.TPMSSignatureECC, *ecdsa.PublicKey, error) {
	sigBytes, err := hex.DecodeString(hexSignature)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid signature hex: %w", err)
	}

	pemHex, err := hex.DecodeString(hexPublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid TPM public key hex: %w", err)
	}

	pubKey, err := parsePEMPublicKey(pemHex)
	if err != nil {
		return nil, nil, fmt.Errorf("parse TPM public key: %w", err)
	}

	tpmSig, err := tpm2.Unmarshal[tpm2.TPMTSignature](sigBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("decode TPMT_SIGNATURE: %w", err)
	}

	if tpmSig.SigAlg != tpm2.TPMAlgECDSA {
		return nil, nil, fmt.Errorf("%w: %v", ErrUnexpectedSignatureAlgorithm, tpmSig.SigAlg)
	}

	ecc, err := tpmSig.Signature.ECDSA()
	if err != nil {
		return nil, nil, fmt.Errorf("ECDSA(): %w", err)
	}

	ecdsaPub, ok := pubKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, nil, ErrTPMSignatureInvalid
	}

	return ecc, ecdsaPub, nil
}

func parsePEMPublicKey(pemBytes []byte) (any, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, ErrNoPEMBlock
	}

	pubKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PEM public key: %w", err)
	}

	return pubKey, nil
}

func parseTPMAttest(hexQuote string, nonce string) (*tpm2.TPMSQuoteInfo, []byte, error) {
	quoteBytes, err := hex.DecodeString(hexQuote)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid quote hex: %w", err)
	}

	att2b := tpm2.BytesAs2B[tpm2.TPMSAttest](quoteBytes)

	att, err := att2b.Contents()
	if err != nil {
		return nil, nil, fmt.Errorf("decode TPMS_ATTEST: %w", err)
	}

	if att.Type != tpm2.TPMSTAttestQuote {
		return nil, nil, fmt.Errorf("%w: %v", ErrUnexpectedAttestationType, att.Type)
	}

	if string(att.ExtraData.Buffer) != nonce {
		return nil, nil, ErrNonceMismatch
	}

	quoteInfo, err := att.Attested.Quote()
	if err != nil {
		return nil, nil, fmt.Errorf("attested.Quote(): %w", err)
	}

	hash := sha256.New()
	_, _ = hash.Write(quoteBytes)
	digest := hash.Sum(nil)

	return quoteInfo, digest, nil
}

func computePCRDigest(quoteInfo *tpm2.TPMSQuoteInfo, pcrs map[int]string) ([]byte, error) {
	if len(quoteInfo.PCRSelect.PCRSelections) == 0 {
		return nil, ErrNoPCRSelection
	}

	// find SHA-256 selection (your client uses SHA-256)
	var sel *tpm2.TPMSPCRSelection
	for i := range quoteInfo.PCRSelect.PCRSelections {
		if quoteInfo.PCRSelect.PCRSelections[i].Hash == tpm2.TPMAlgSHA256 {
			sel = &quoteInfo.PCRSelect.PCRSelections[i]

			break
		}
	}

	if sel == nil {
		return nil, ErrNoPCRSelection
	}

	indices := pcrIndicesFromSelection(*sel)

	recomputed, err := pcrDigest(pcrs, indices)
	if err != nil {
		return nil, fmt.Errorf("compute PCR digest: %w", err)
	}

	return recomputed, nil
}

// Reference implementation https://github.com/google/go-tpm/blob/main/legacy/tpm2/tpm2.go#L75
func pcrIndicesFromSelection(sel tpm2.TPMSPCRSelection) []int {
	var indices []int

	for byteIdx, b := range sel.PCRSelect {
		for bit := range 8 {
			if b&(1<<bit) != 0 {
				indices = append(indices, byteIdx*8+bit)
			}
		}
	}

	sort.Ints(indices)

	return indices
}

func pcrDigest(pcrs map[int]string, indices []int) ([]byte, error) {
	hash := sha256.New()

	for _, idx := range indices {
		value, ok := pcrs[idx]
		if !ok {
			return nil, fmt.Errorf("%w: %d", ErrMissingPCR, idx)
		}

		if len(value) >= 2 && (value[0:2] == "0x" || value[0:2] == "0X") {
			value = value[2:]
		}

		raw, err := hex.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("PCR %d hex decode: %w", idx, err)
		}

		_, _ = hash.Write(raw)
	}

	return hash.Sum(nil), nil
}
