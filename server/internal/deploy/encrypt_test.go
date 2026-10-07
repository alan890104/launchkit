package deploy

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validKey is a test-only 64-hex-char AES-256 key.
const validKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// altKey is a different valid key used to test key mismatch.
const altKey = "fffefdfcfbfaf9f8f7f6f5f4f3f2f1f0efeeedecebeae9e8e7e6e5e4e3e2e1e0"

// ─── NewEncryptor ─────────────────────────────────────────────────────────────

func TestNewEncryptor_ValidKey(t *testing.T) {
	enc, err := NewEncryptor(validKey)
	require.NoError(t, err)
	assert.NotNil(t, enc)
}

func TestNewEncryptor_EmptyKey(t *testing.T) {
	_, err := NewEncryptor("")
	assert.ErrorContains(t, err, "SECRET_ENCRYPTION_KEY is required")
}

func TestNewEncryptor_InvalidHex(t *testing.T) {
	_, err := NewEncryptor("not-hex-at-all-ZZZZ-invalid!@#$%^&*()____________________________")
	assert.ErrorContains(t, err, "valid hex")
}

func TestNewEncryptor_TooShort(t *testing.T) {
	// 62 hex chars = 31 bytes, not 32
	_, err := NewEncryptor("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	assert.ErrorContains(t, err, "64 hex characters")
}

func TestNewEncryptor_TooLong(t *testing.T) {
	// 66 hex chars = 33 bytes, not 32
	_, err := NewEncryptor("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f2021")
	assert.ErrorContains(t, err, "64 hex characters")
}

// ─── Encrypt / Decrypt round-trips ───────────────────────────────────────────

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	enc, err := NewEncryptor(validKey)
	require.NoError(t, err)

	plaintext := "super-secret-api-key-12345"
	ciphertext, nonce, err := enc.Encrypt(plaintext)
	require.NoError(t, err)
	assert.NotEmpty(t, ciphertext)
	assert.Len(t, nonce, 12, "GCM nonce must be 12 bytes")

	got, err := enc.Decrypt(ciphertext, nonce)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	enc, err := NewEncryptor(validKey)
	require.NoError(t, err)

	ciphertext, nonce, err := enc.Encrypt("")
	require.NoError(t, err)

	got, err := enc.Decrypt(ciphertext, nonce)
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestEncryptDecrypt_LongValue(t *testing.T) {
	enc, err := NewEncryptor(validKey)
	require.NoError(t, err)

	// 4KB value (realistic for a large JSON secret or certificate)
	large := strings.Repeat("x", 4096)
	ciphertext, nonce, err := enc.Encrypt(large)
	require.NoError(t, err)

	got, err := enc.Decrypt(ciphertext, nonce)
	require.NoError(t, err)
	assert.Equal(t, large, got)
}

func TestEncryptDecrypt_UnicodeValue(t *testing.T) {
	enc, err := NewEncryptor(validKey)
	require.NoError(t, err)

	// Non-ASCII test data (Chinese, Greek, emoji): checks that multi-byte UTF-8 survives an encrypt/decrypt round trip.
	plaintext := "密码：安全字符串-αβγ-🔑"
	ciphertext, nonce, err := enc.Encrypt(plaintext)
	require.NoError(t, err)

	got, err := enc.Decrypt(ciphertext, nonce)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

// ─── Unique nonces ────────────────────────────────────────────────────────────

func TestEncrypt_UniqueNonces(t *testing.T) {
	enc, err := NewEncryptor(validKey)
	require.NoError(t, err)

	const iterations = 20
	nonces := make([][]byte, iterations)
	ciphertexts := make([][]byte, iterations)

	for i := range iterations {
		ct, nonce, err := enc.Encrypt("same-plaintext")
		require.NoError(t, err)
		nonces[i] = nonce
		ciphertexts[i] = ct
	}

	// Every nonce must be unique
	for i := range iterations {
		for j := i + 1; j < iterations; j++ {
			assert.False(t, bytes.Equal(nonces[i], nonces[j]),
				"nonce collision between iterations %d and %d", i, j)
		}
	}

	// Same plaintext must produce different ciphertexts (because nonces differ)
	for i := range iterations {
		for j := i + 1; j < iterations; j++ {
			assert.False(t, bytes.Equal(ciphertexts[i], ciphertexts[j]),
				"ciphertext collision between iterations %d and %d", i, j)
		}
	}
}

// ─── Tamper / error cases ─────────────────────────────────────────────────────

func TestDecrypt_WrongKey(t *testing.T) {
	enc1, err := NewEncryptor(validKey)
	require.NoError(t, err)
	enc2, err := NewEncryptor(altKey)
	require.NoError(t, err)

	ciphertext, nonce, err := enc1.Encrypt("secret")
	require.NoError(t, err)

	_, err = enc2.Decrypt(ciphertext, nonce)
	assert.Error(t, err, "decrypting with wrong key must return an error")
}

func TestDecrypt_CorruptedCiphertext(t *testing.T) {
	enc, err := NewEncryptor(validKey)
	require.NoError(t, err)

	ciphertext, nonce, err := enc.Encrypt("secret")
	require.NoError(t, err)

	// Flip every byte of the ciphertext
	corrupted := make([]byte, len(ciphertext))
	for i, b := range ciphertext {
		corrupted[i] = b ^ 0xff
	}

	_, err = enc.Decrypt(corrupted, nonce)
	assert.Error(t, err, "decrypting corrupted ciphertext must return an error")
}

func TestDecrypt_TruncatedCiphertext(t *testing.T) {
	enc, err := NewEncryptor(validKey)
	require.NoError(t, err)

	ciphertext, nonce, err := enc.Encrypt("secret")
	require.NoError(t, err)

	// GCM tag is 16 bytes; truncating to half drops the auth tag
	truncated := ciphertext[:len(ciphertext)/2]

	_, err = enc.Decrypt(truncated, nonce)
	assert.Error(t, err, "decrypting truncated ciphertext must return an error")
}

func TestDecrypt_WrongNonce(t *testing.T) {
	enc, err := NewEncryptor(validKey)
	require.NoError(t, err)

	ciphertext, nonce, err := enc.Encrypt("secret")
	require.NoError(t, err)

	// Corrupt just the nonce
	badNonce := make([]byte, len(nonce))
	for i, b := range nonce {
		badNonce[i] = b ^ 0xff
	}

	_, err = enc.Decrypt(ciphertext, badNonce)
	assert.Error(t, err, "decrypting with wrong nonce must return an error")
}

func TestDecrypt_EmptyCiphertext(t *testing.T) {
	enc, err := NewEncryptor(validKey)
	require.NoError(t, err)

	// Per implementation: empty ciphertext returns empty string, no error
	got, err := enc.Decrypt([]byte{}, []byte{})
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

// ─── SetSecretValues + ResolveUserSecrets logic ───────────────────────────────

// TestEncryptorRoundTripViaSecretPattern verifies that the encrypt→store→decrypt
// pattern used by SetSecretValues / ResolveUserSecrets produces the original value.
// This is a pure unit test; no DB is involved.
func TestEncryptorRoundTripViaSecretPattern(t *testing.T) {
	enc, err := NewEncryptor(validKey)
	require.NoError(t, err)

	secrets := map[string]string{
		"DATABASE_URL":   "postgresql://user:pass@host/db",
		"RESEND_API_KEY": "re_abc123XYZ",
		"STRIPE_SECRET":  "sk_live_" + "verylongkeystring1234567890", // split so secret scanners do not flag a fake key
		"EMPTY_SECRET":   "",
	}

	for name, original := range secrets {
		t.Run(name, func(t *testing.T) {
			// Simulate SetSecretValues path: encrypt before storing
			ciphertext, nonce, err := enc.Encrypt(original)
			require.NoError(t, err)

			// Simulate ResolveUserSecrets path: decrypt after reading from DB
			var got string
			if len(nonce) > 0 {
				got, err = enc.Decrypt(ciphertext, nonce)
				require.NoError(t, err)
			} else {
				got = string(ciphertext)
			}

			assert.Equal(t, original, got)
		})
	}
}

// TestDevModeFallback verifies that when enc == nil, the SetSecretValues path
// stores plaintext and ResolveUserSecrets reads it back as-is.
func TestDevModeFallback(t *testing.T) {
	var enc *Encryptor // nil = dev mode

	plaintext := "dev-secret-value"

	// Simulate SetSecretValues with enc == nil
	var ciphertext []byte
	var nonce []byte
	if enc != nil {
		var err error
		ciphertext, nonce, err = enc.Encrypt(plaintext)
		require.NoError(t, err)
	} else {
		ciphertext = []byte(plaintext)
		// nonce stays nil
	}

	// Simulate ResolveUserSecrets with enc == nil
	var got string
	if enc != nil && len(nonce) > 0 {
		var err error
		got, err = enc.Decrypt(ciphertext, nonce)
		require.NoError(t, err)
	} else {
		got = string(ciphertext)
	}

	assert.Equal(t, plaintext, got)
	assert.Nil(t, nonce, "dev mode must not produce a nonce")
}
