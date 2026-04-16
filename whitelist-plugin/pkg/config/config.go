package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

// ServerConfig is used by the centralized whitelist server (cmd/server).
type ServerConfig struct {
	DgraphGraphQLURL  string        `mapstructure:"dgraph_graphql_url"`
	RefreshInterval   time.Duration `mapstructure:"refresh_interval"`
	RefreshRetryCount int           `mapstructure:"refresh_retry_count"`
	IdleConnTimeout   time.Duration `mapstructure:"idle_conn_timeout"`
	HTTPTimeout       time.Duration `mapstructure:"http_timeout"`
	QueryTimeout      time.Duration `mapstructure:"query_timeout"`
	ServerListenAddr  string        `mapstructure:"server_listen_addr"`
	Debug             bool          `mapstructure:"debug"`
}

// ClientConfig is used by the thin whitelist plugin (cmd/whitelist).
type ClientConfig struct {
	ServerURL    string        `mapstructure:"server_url"`
	CheckTimeout time.Duration `mapstructure:"check_timeout"`
}

func LoadServerConfig() (*ServerConfig, error) {
	v := viper.New()

	configDir, err := ensureConfigDir()
	if err != nil {
		return nil, err
	}

	v.SetConfigName("whitelist")
	v.SetConfigType("yaml")
	v.AddConfigPath(configDir)

	v.SetDefault("dgraph_graphql_url", "http://localhost:8080/graphql")
	v.SetDefault("refresh_interval", "6h")
	v.SetDefault("refresh_retry_count", 3)
	v.SetDefault("idle_conn_timeout", "90s")
	v.SetDefault("http_timeout", "30s")
	v.SetDefault("query_timeout", "20m")
	v.SetDefault("server_listen_addr", ":8081")
	v.SetDefault("debug", true)

	if err := readConfig(v, configDir, "whitelist.yaml"); err != nil {
		return nil, err
	}

	var cfg ServerConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to decode config: %w", err)
	}

	return &cfg, nil
}

func LoadClientConfig() (*ClientConfig, error) {
	v := viper.New()

	configDir, err := ensureConfigDir()
	if err != nil {
		return nil, err
	}

	v.SetConfigName("whitelist")
	v.SetConfigType("yaml")
	v.AddConfigPath(configDir)

	v.SetDefault("server_url", "http://localhost:8081")
	v.SetDefault("check_timeout", "2s")

	if err := readConfig(v, configDir, "whitelist.yaml"); err != nil {
		return nil, err
	}

	var cfg ClientConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to decode config: %w", err)
	}

	return &cfg, nil
}

func ensureConfigDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}

	configDir := filepath.Join(homeDir, "deepfry")
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create config directory %s: %w", configDir, err)
		}
	}

	return configDir, nil
}

func readConfig(v *viper.Viper, configDir, filename string) error {
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("error reading config file: %w", err)
		}
		log.Printf("No config file found, using defaults")

		configPath := filepath.Join(configDir, filename)
		for _, key := range v.AllKeys() {
			v.Set(key, v.Get(key))
		}
		if err := v.SafeWriteConfigAs(configPath); err != nil {
			log.Printf("Warning: Failed to write default config to %s: %v", configPath, err)
		} else {
			log.Printf("Created default configuration file at %s", configPath)
		}
	} else {
		log.Printf("Using config file: %s", v.ConfigFileUsed())
	}

	return nil
}
