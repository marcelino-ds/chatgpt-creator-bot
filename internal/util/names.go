package util

import (
	"fmt"
	"math/rand"

	"github.com/brianvoe/gofakeit/v7"
)

// RandomName returns a random first and last name using gofakeit.
func RandomName() (string, string) {
	return gofakeit.FirstName(), gofakeit.LastName()
}

// RandomBirthdate returns a random birthdate string in YYYY-MM-DD format from 1985-2002.
func RandomBirthdate() string {
	year := rand.Intn(2002-1985+1) + 1985
	month := rand.Intn(12) + 1
	day := rand.Intn(28) + 1
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}
