package identity

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
)

func randRead(b []byte) (int, error) { return rand.Read(b) }

func cryptoRandInt(max int64) (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(max))
	if err != nil {
		return 0, fmt.Errorf("generate random integer: %w", err)
	}
	return n.Int64(), nil
}

func base64URLEncode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
