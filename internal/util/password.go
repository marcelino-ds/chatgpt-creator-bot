package util

import (
	"math/rand"
)

const (
	lowerChars   = "abcdefghijklmnopqrstuvwxyz"
	upperChars   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digitChars   = "0123456789"
	specialChars = "!@#$%&*"
	allChars     = lowerChars + upperChars + digitChars + specialChars
)

// GeneratePassword generates a random password of given length (default 14).
// It guarantees at least 1 lowercase, 1 uppercase, 1 digit, and 1 special character.
func GeneratePassword(length int) string {
	if length <= 0 {
		length = 14
	}

	password := make([]byte, length)
	password[0] = lowerChars[rand.Intn(len(lowerChars))]
	password[1] = upperChars[rand.Intn(len(upperChars))]
	password[2] = digitChars[rand.Intn(len(digitChars))]
	password[3] = specialChars[rand.Intn(len(specialChars))]

	for i := 4; i < length; i++ {
		password[i] = allChars[rand.Intn(len(allChars))]
	}

	rand.Shuffle(len(password), func(i, j int) {
		password[i], password[j] = password[j], password[i]
	})

	return string(password)
}
