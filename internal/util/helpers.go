package util

import (
	"math/rand"

	"github.com/google/uuid"
)

const alphanumeric = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandStr generates a random alphanumeric string of given length.
//
// It uses the package-level source, which is auto-seeded and safe for
// concurrent use. Creating a per-call source from time.Now() collided across
// workers because the Windows clock resolution is coarser than goroutine
// start times, producing duplicate emails and passwords.
func RandStr(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = alphanumeric[rand.Intn(len(alphanumeric))]
	}
	return string(b)
}

// GenerateUUID generates a random UUID.
func GenerateUUID() string {
	return uuid.New().String()
}
