package auth

import "golang.org/x/crypto/bcrypt"

// TemporaryCredentials uses the same account-ID and password rules as registration.
func TemporaryCredentials(username, password string) (string, string, error) {
	name, err := normalizeUsername(username, false)
	if err != nil || !validPassword(password) {
		return "", "", ErrInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return name, string(hash), err
}
