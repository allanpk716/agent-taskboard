package identity

import (
	"crypto/rand"
	"encoding/base64"
)

func randRead(b []byte) (int, error) { return rand.Read(b) }

func base64URLEncode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
