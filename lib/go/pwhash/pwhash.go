package pwhash

type PasswordHasher interface {
	Hash(password string) (string, error)
	Compare(hashedPassword, password string) error
}

func New() PasswordHasher {
	return &argon2idHasher{}
}
