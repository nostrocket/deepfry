package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// envKeyReplacer maps viper's dotted config keys to env-var style:
// quarantine.relay_url -> QUARANTINE_RELAY_URL (joined with SetEnvPrefix).
func envKeyReplacer() *strings.Replacer {
	return strings.NewReplacer(".", "_")
}

// QuarantineConfig controls the router plugin's side-channel to the quarantine relay.
type QuarantineConfig struct {
	Enabled         bool          `mapstructure:"enabled"`
	RelayURL        string        `mapstructure:"relay_url"`
	BufferSize      int           `mapstructure:"buffer_size"`
	PublishTimeout  time.Duration `mapstructure:"publish_timeout"`
	MetricsInterval time.Duration `mapstructure:"metrics_interval"`
}

// RouterConfig is used by the router plugin (cmd/router).
// It embeds the thin whitelist client config plus a quarantine section.
type RouterConfig struct {
	ServerURL    string           `mapstructure:"server_url"`
	CheckTimeout time.Duration    `mapstructure:"check_timeout"`
	Quarantine   QuarantineConfig `mapstructure:"quarantine"`
}

// LoadRouterConfig loads ~/deepfry/router.yaml, applying defaults and env overrides.
// Env prefix: ROUTER_ (e.g. ROUTER_QUARANTINE_ENABLED, ROUTER_QUARANTINE_RELAY_URL).
func LoadRouterConfig() (*RouterConfig, error) {
	v := viper.New()

	configDir, err := ensureConfigDir()
	if err != nil {
		return nil, err
	}

	v.SetConfigName("router")
	v.SetConfigType("yaml")
	v.AddConfigPath(configDir)

	v.SetDefault("server_url", "http://localhost:8081")
	v.SetDefault("check_timeout", "2s")
	v.SetDefault("quarantine.enabled", true)
	v.SetDefault("quarantine.relay_url", "ws://strfry-quarantine:7778")
	v.SetDefault("quarantine.buffer_size", 10000)
	v.SetDefault("quarantine.publish_timeout", "5s")
	v.SetDefault("quarantine.metrics_interval", "60s")

	v.SetEnvPrefix("ROUTER")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(envKeyReplacer())

	if err := readConfig(v, configDir, "router.yaml"); err != nil {
		return nil, err
	}

	var cfg RouterConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to decode router config: %w", err)
	}

	return &cfg, nil
}
