package repository

// This file is intentionally left blank.
type KeyRepository interface {
	GetAll() ([][32]byte, error)
}
