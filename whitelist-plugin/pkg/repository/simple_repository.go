package repository

import "encoding/hex"

type SimpleRepository struct {
	keys [][32]byte
}

func NewSimpleRepository() *SimpleRepository {
	pubKeys := []string{
		"e56c3d2fd8e0309264e558309ae65e31328fe0a2e1fe26cf4074a8ff1df48fc2", //eventforwarder
		"d91191e30e00444b942c0e82cad470b32af171764c2275bee0bd99377efd4075", //gsov
		"a0dda882fb89732b04793a2c989435fcd89ee559e81291074450edbd9b15621b", //rocketdog8
	}

	keys := make([][32]byte, 0, len(pubKeys))
	for _, s := range pubKeys {
		keys = append(keys, hexTo32ByteArray(s))
	}

	return &SimpleRepository{keys: keys}
}

func (r *SimpleRepository) GetAll() ([][32]byte, error) {
	return r.keys, nil
}

// hexTo32ByteArray decodes a 64-character hex string to a [32]byte array.
func hexTo32ByteArray(hexStr string) [32]byte {
	var arr [32]byte
	data, err := hex.DecodeString(hexStr)
	if err != nil || len(data) != 32 {
		panic("invalid hex string for 32-byte array") // Or handle error as needed
	}
	copy(arr[:], data)
	return arr
}
