package stringutil

import (
	"crypto/rand"
	"math/big"
)

const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

var lettersLen = big.NewInt(int64(len(letters)))

// RandomAlphanumeric returns a random alphanumeric string of length n.
func RandomAlphanumeric(n int) string {
	b := make([]byte, n)

	for i := range b {
		num, err := rand.Int(rand.Reader, lettersLen)
		if err != nil {
			panic(err)
		}

		b[i] = letters[num.Int64()]
	}

	return string(b)
}
