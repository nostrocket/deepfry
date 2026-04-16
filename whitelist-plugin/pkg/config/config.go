package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	DgraphGraphQLURL  string        `mapstructure:"dgraph_graphql_url"`
	RefreshInterval   time.Duration `mapstructure:"refresh_interval"`
	RefreshRetryCount int           `mapstructure:"refresh_retry_count"`
	IdleConnTimeout   time.Duration `mapstructure:"idle_conn_timeout"`
	HTTPTimeout       time.Duration `mapstructure:"http_timeout"`
	QueryTimeout      time.Duration `mapstructure:"query_timeout"`
}

func LoadConfig() (*Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("could not determine home directory: %w", err)
	}

	configDir := filepath.Join(homeDir, "deepfry")
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create config directory %s: %w", configDir, err)
		}
	}

	viper.SetConfigName("whitelist")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(configDir)

	viper.SetDefault("dgraph_graphql_url", "http://localhost:8080/graphql")
	viper.SetDefault("refresh_interval", "6h")
	viper.SetDefault("refresh_retry_count", 3)
	viper.SetDefault("idle_conn_timeout", "90s")
	viper.SetDefault("http_timeout", "30s")
	viper.SetDefault("query_timeout", "20m")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		log.Printf("No config file found, using defaults")

		configPath := filepath.Join(configDir, "whitelist.yaml")
		// viper.SafeWriteConfigAs does not write SetDefault values,
		// so promote them to explicit values before writing.
		for _, key := range viper.AllKeys() {
			viper.Set(key, viper.Get(key))
		}
		if err := viper.SafeWriteConfigAs(configPath); err != nil {
			log.Printf("Warning: Failed to write default config to %s: %v", configPath, err)
		} else {
			log.Printf("Created default configuration file at %s", configPath)
		}
	} else {
		log.Printf("Using config file: %s", viper.ConfigFileUsed())
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to decode config: %w", err)
	}

	return &cfg, nil
}
