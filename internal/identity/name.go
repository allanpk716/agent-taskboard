package identity

import "strings"

// HasReplacementChar reports whether a display name was damaged by decoding it
// with the wrong terminal or filesystem encoding.
func HasReplacementChar(name string) bool {
	return strings.ContainsRune(name, '�')
}
