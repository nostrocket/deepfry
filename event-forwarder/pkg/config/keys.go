package config

// Configuration key constants
// These constants centralize all environment variable and configuration key names
// to eliminate magic strings and improve maintainability.

const (
	// Core service configuration keys
	KeySourceRelayURL  = "SOURCE_RELAY_URL"
	KeyDeepFryRelayURL = "DEEPFRY_RELAY_URL"
	KeyNostrSecretKey  = "NOSTR_SYNC_SECKEY"

	// UI configuration keys
	KeyQuietMode = "QUIET_MODE"

	// Sync configuration keys
	KeySyncWindowSeconds        = "SYNC_WINDOW_SECONDS"
	KeySyncMaxBatch             = "SYNC_MAX_BATCH"
	KeySyncMaxCatchupLagSeconds = "SYNC_MAX_CATCHUP_LAG_SECONDS"
	KeySyncStartTime            = "SYNC_START_TIME"

	// Network configuration keys
	KeyNetworkInitialBackoffSeconds = "NETWORK_INITIAL_BACKOFF_SECONDS"
	KeyNetworkMaxBackoffSeconds     = "NETWORK_MAX_BACKOFF_SECONDS"
	KeyNetworkBackoffJitter         = "NETWORK_BACKOFF_JITTER"

	// Timeout configuration keys
	KeyTimeoutPublishSeconds   = "TIMEOUT_PUBLISH_SECONDS"
	KeyTimeoutSubscribeSeconds = "TIMEOUT_SUBSCRIBE_SECONDS"
)

// Default values for configuration
const (
	// Sync defaults
	DefaultSyncWindowSeconds        = 5
	DefaultSyncMaxBatch             = 1000
	DefaultSyncMaxCatchupLagSeconds = 10
	DefaultSyncStartTime            = "" // Empty means start from recent

	// Network defaults
	DefaultNetworkInitialBackoffSeconds = 1
	DefaultNetworkMaxBackoffSeconds     = 30
	DefaultNetworkBackoffJitter         = 0.2

	// UI defaults
	DefaultQuietMode = false

	// Timeout defaults
	DefaultTimeoutPublishSeconds   = 10
	DefaultTimeoutSubscribeSeconds = 10
)

// CLI flag name constants
const (
	// CLI flag names (kebab-case for command line)
	FlagSourceRelayURL               = "source"
	FlagDeepFryRelayURL              = "deepfry"
	FlagNostrSecretKey               = "secret-key"
	FlagQuietMode                    = "quiet"
	FlagSyncWindowSeconds            = "sync-window-seconds"
	FlagSyncMaxBatch                 = "sync-max-batch"
	FlagSyncMaxCatchupLagSeconds     = "sync-max-catchup-lag-seconds"
	FlagSyncStartTime                = "sync-start-time"
	FlagNetworkInitialBackoffSeconds = "network-initial-backoff-seconds"
	FlagNetworkMaxBackoffSeconds     = "network-max-backoff-seconds"
	FlagNetworkBackoffJitter         = "network-backoff-jitter"
	FlagTimeoutPublishSeconds        = "timeout-publish-seconds"
	FlagTimeoutSubscribeSeconds      = "timeout-subscribe-seconds"
	FlagHelp                         = "help"
)

// Help message constants
const (
	AppName        = "Event Forwarder"
	AppDescription = "Forward events between Nostr relays"
	UsageFormat    = "fwd [OPTIONS]"

	// Help descriptions
	HelpSourceRelayURL               = "Source relay URL (required)"
	HelpDeepFryRelayURL              = "DeepFry relay URL (required)"
	HelpNostrSecretKey               = "Nostr secret key (required)"
	HelpQuietMode                    = "Run in quiet mode (no TUI, log to stdout/stderr)"
	HelpSyncWindowSeconds            = "Sync window in seconds"
	HelpSyncMaxBatch                 = "Max sync batch size"
	HelpSyncMaxCatchupLagSeconds     = "Max catchup lag in seconds"
	HelpSyncStartTime                = "Sync start time (RFC3339 format, e.g., 2020-01-01T00:00:00Z)"
	HelpNetworkInitialBackoffSeconds = "Initial backoff in seconds"
	HelpNetworkMaxBackoffSeconds     = "Max backoff in seconds"
	HelpNetworkBackoffJitter         = "Backoff jitter"
	HelpTimeoutPublishSeconds        = "Publish timeout in seconds"
	HelpTimeoutSubscribeSeconds      = "Subscribe timeout in seconds"
	HelpShowHelp                     = "Show this help message"

	// Environment variable descriptions (reuse help descriptions)
	EnvDescSourceRelayURL               = "Source relay URL"
	EnvDescDeepFryRelayURL              = "DeepFry relay URL"
	EnvDescNostrSecretKey               = "Nostr secret key"
	EnvDescQuietMode                    = "Run in quiet mode (no TUI)"
	EnvDescSyncWindowSeconds            = "Sync window in seconds"
	EnvDescSyncMaxBatch                 = "Max sync batch size"
	EnvDescSyncMaxCatchupLagSeconds     = "Max catchup lag in seconds"
	EnvDescSyncStartTime                = "Sync start time (RFC3339 format)"
	EnvDescNetworkInitialBackoffSeconds = "Initial backoff in seconds"
	EnvDescNetworkMaxBackoffSeconds     = "Max backoff in seconds"
	EnvDescNetworkBackoffJitter         = "Backoff jitter"
	EnvDescTimeoutPublishSeconds        = "Publish timeout in seconds"
	EnvDescTimeoutSubscribeSeconds      = "Subscribe timeout in seconds"

	// Help section headers
	HelpOptions         = "Options:"
	HelpEnvironmentVars = "Environment Variables:"
	HelpUsage           = "Usage:"
	HelpNote            = "Note: CLI options override environment variables"
)
