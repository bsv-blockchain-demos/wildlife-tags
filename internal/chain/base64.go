package chain

import "encoding/base64"

// base64Std wraps the standard encoding because BRC-29 derivation components
// are specified as standard base64 with padding, not the URL-safe variant the
// rest of this program uses for tag secrets. Mixing the two produces a
// derivation mismatch that only shows up as an unspendable payment.
func base64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
