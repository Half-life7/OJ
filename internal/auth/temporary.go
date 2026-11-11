package auth

import "golang.org/x/crypto/bcrypt"

// TemporaryUsername validates and normalizes a contest account ID without
// paying the bcrypt cost during CSV preview.
func TemporaryUsername(username string) (string, error) {
	return normalizeUsername(username, false)
}

// TemporaryCredentials uses the same account-ID and password rules as registration.
func TemporaryCredentials(username, password string) (string, string, error) {
	name, err := TemporaryUsername(username)
	if err != nil || !validPassword(password) {
		return "", "", ErrInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return name, string(hash), err
}
