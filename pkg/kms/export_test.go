package kms

// Exported aliases for black-box testing.
//
//nolint:gochecknoglobals // test exports
var (
	Encrypt            = encrypt
	Decrypt            = decrypt
	BuildAAD           = buildAAD
	ParseVolumeKeySets = parseVolumeKeySets
	ExtractClientIP    = extractClientIP
	SanitizeIP         = sanitizeIP
)

const (
	KeySize      = keySize
	AADNonceSize = aadNonceSize
)

// Suffix constants for building test secret data.
const (
	KeySuffix       = keySuffix
	NonceSuffix     = nonceSuffix
	LuksKeySuffix   = luksKeySuffix
	SealedFromIPKey = sealedFromIPKey
)
