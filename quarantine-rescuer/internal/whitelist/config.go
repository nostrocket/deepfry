package whitelist

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

// Config holds the subset of ~/deepfry/whitelist.yaml the rescuer needs.
// Field names mirror the live plugin's ClientConfig
// (whitelist-plugin/pkg/config) so the same yaml file works for both.
type Config struct {
	ServerURL    string        `mapstructure:"server_url"`
	CheckTimeout time.Duration `mapstructure:"check_timeout"`
}

// LoadConfig reads ~/deepfry/whitelist.yaml using the same conventions as
// the live whitelist plugin. Missing file → defaults are used; the file is
// not auto-created (we don't want the rescuer to silently materialise a
// blank config on a host that's just missing it).
func LoadConfig() (*Config, error) {
	v := viper.New()

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("could not determine home directory: %w", err)
	}
	configDir := filepath.Join(homeDir, "deepfry")

	v.SetConfigName("whitelist")
	v.SetConfigType("yaml")
	v.AddConfigPath(configDir)

	v.SetDefault("server_url", "http://localhost:8081")
	v.SetDefault("check_timeout", "2s")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	return &cfg, nil
}
