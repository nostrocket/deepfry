package repository

import (
	"encoding/hex"
	"fmt"
)

// This file is intentionally left blank.
type KeyRepository interface {
	GetAll() ([][32]byte, error)
}

// hexTo32ByteArray decodes a 64-character hex string to a [32]byte array.
func hexTo32ByteArray(hexStr string) ([32]byte, error) {
	var arr [32]byte
	data, err := hex.DecodeString(hexStr)
	if err != nil || len(data) != 32 {
		return arr, fmt.Errorf("invalid hex string for 32-byte array: %s", data)
	}
	copy(arr[:], data)
	return arr, nil
}
