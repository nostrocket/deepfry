package repository

import "fmt"

type SimpleRepository struct {
	keys [][32]byte
}

func NewSimpleRepository() *SimpleRepository {
	pubKeys := []string{
		"e56c3d2fd8e0309264e558309ae65e31328fe0a2e1fe26cf4074a8ff1df48fc2", //eventforwarder
		"d91191e30e00444b942c0e82cad470b32af171764c2275bee0bd99377efd4075", //gsov
		"a0dda882fb89732b04793a2c989435fcd89ee559e81291074450edbd9b15621b", //rocketdog8
		"ba1838441e720ee91360d38321a19cbf8596e6540cfa045c9c5d429f1a2b9e3a", //macro88
	}

	keys := make([][32]byte, 0, len(pubKeys))
	for _, s := range pubKeys {
		k, err := hexTo32ByteArray(s)
		if err != nil {
			panic(fmt.Errorf("failed to convert pubkey to 32-byte array: %w", err))
		}
		keys = append(keys, k)
	}

	return &SimpleRepository{keys: keys}
}

func (r *SimpleRepository) GetAll() ([][32]byte, error) {
	return r.keys, nil
}
