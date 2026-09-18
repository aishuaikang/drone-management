package counterstrike

import (
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/tjfoc/gmsm/sm4"
)

func encryptSM4CBC(plaintext []byte, keyText, ivText string) ([]byte, error) {
	key, err := decodeSM4Material(keyText)
	if err != nil {
		return nil, fmt.Errorf("SM4 key: %w", err)
	}
	iv, err := decodeSM4Material(ivText)
	if err != nil {
		return nil, fmt.Errorf("SM4 IV: %w", err)
	}
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create SM4 cipher: %w", err)
	}
	padded := pkcs7Pad(plaintext, block.BlockSize())
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(ciphertext)))
	base64.StdEncoding.Encode(encoded, ciphertext)
	return encoded, nil
}

func decodeSM4Material(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	hexValue := strings.TrimPrefix(strings.TrimPrefix(value, "hex:"), "HEX:")
	if len(hexValue) == 32 {
		decoded, err := hex.DecodeString(hexValue)
		if err == nil {
			return decoded, nil
		}
	}
	if len([]byte(value)) != sm4.BlockSize {
		return nil, fmt.Errorf("must be 16 UTF-8 bytes or 32 hexadecimal characters")
	}
	return []byte(value), nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for index := len(data); index < len(out); index++ {
		out[index] = byte(padding)
	}
	return out
}
