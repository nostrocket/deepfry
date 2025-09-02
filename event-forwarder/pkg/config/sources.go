package config

import (
	"os"
	"strconv"
)

// ConfigSource represents a source of configuration values
type ConfigSource interface {
	GetString(key string) (string, bool)
	GetInt(key string) (int, bool)
	GetFloat(key string) (float64, bool)
}

// EnvSource implements ConfigSource for environment variables
type EnvSource struct{}

func (e *EnvSource) GetString(key string) (string, bool) {
	value := os.Getenv(key)
	return value, value != ""
}

func (e *EnvSource) GetInt(key string) (int, bool) {
	value := os.Getenv(key)
	if value == "" {
		return 0, false
	}
	if i, err := strconv.Atoi(value); err == nil {
		return i, true
	}
	return 0, false
}

func (e *EnvSource) GetFloat(key string) (float64, bool) {
	value := os.Getenv(key)
	if value == "" {
		return 0, false
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return f, true
	}
	return 0, false
}

// FlagSource implements ConfigSource for command-line flags
type FlagSource struct {
	values map[string]interface{}
}

func NewFlagSource() *FlagSource {
	return &FlagSource{values: make(map[string]interface{})}
}

func (f *FlagSource) Set(key string, value interface{}) {
	f.values[key] = value
}

func (f *FlagSource) GetString(key string) (string, bool) {
	if value, exists := f.values[key]; exists {
		if str, ok := value.(string); ok && str != "" {
			return str, true
		}
	}
	return "", false
}

func (f *FlagSource) GetInt(key string) (int, bool) {
	if value, exists := f.values[key]; exists {
		if i, ok := value.(int); ok {
			return i, true
		}
	}
	return 0, false
}

func (f *FlagSource) GetFloat(key string) (float64, bool) {
	if value, exists := f.values[key]; exists {
		if fl, ok := value.(float64); ok {
			return fl, true
		}
	}
	return 0, false
}
