package deploy

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// Encryptor handles AES-256-GCM encryption for secrets at rest.
// The same key must be used for both encrypt and decrypt — rotating keys
// requires re-encrypting all existing secrets.
type Encryptor struct {
	gcm cipher.AEAD
}

// NewEncryptor creates an Encryptor from a 64-character hex-encoded 32-byte key.
// Use `openssl rand -hex 32` to generate a suitable key.
func NewEncryptor(hexKey string) (*Encryptor, error) {
	if hexKey == "" {
		return nil, fmt.Errorf("SECRET_ENCRYPTION_KEY is required for secret encryption")
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("SECRET_ENCRYPTION_KEY must be valid hex: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("SECRET_ENCRYPTION_KEY must be 64 hex characters (32 bytes), got %d bytes", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &Encryptor{gcm: gcm}, nil
}

// Encrypt encrypts plaintext using AES-256-GCM.
// Returns (ciphertext, nonce, error). Both ciphertext and nonce must be stored
// to decrypt later — store them in the value_enc and nonce columns respectively.
func (e *Encryptor) Encrypt(plaintext string) (ciphertext []byte, nonce []byte, err error) {
	nonce = make([]byte, e.gcm.NonceSize()) // 12 bytes for GCM
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext = e.gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return ciphertext, nonce, nil
}

// Decrypt decrypts AES-256-GCM ciphertext using the stored nonce.
func (e *Encryptor) Decrypt(ciphertext, nonce []byte) (string, error) {
	if len(ciphertext) == 0 {
		return "", nil
	}
	plaintext, err := e.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w (key mismatch or corrupted ciphertext)", err)
	}
	return string(plaintext), nil
}
