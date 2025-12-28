package stringutil

import (
	"crypto/rand"
	"math/big"
)

const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandomAlphanumeric returns a random alphanumeric string of length n.
func RandomAlphanumeric(n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(letters)))
	for i := range b {
		num, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b[i] = letters[num.Int64()]
	}
	return string(b)
}
