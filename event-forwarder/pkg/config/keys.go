package config

// Configuration key constants
// These constants centralize all environment variable and configuration key names
// to eliminate magic strings and improve maintainability.

const (
	// Core service configuration keys
	KeySourceRelayURL  = "SOURCE_RELAY_URL"
	KeyDeepFryRelayURL = "DEEPFRY_RELAY_URL"
	KeyNostrSecretKey  = "NOSTR_SYNC_SECKEY"

	// Sync configuration keys
	KeySyncWindowSeconds        = "SYNC_WINDOW_SECONDS"
	KeySyncMaxBatch             = "SYNC_MAX_BATCH"
	KeySyncMaxCatchupLagSeconds = "SYNC_MAX_CATCHUP_LAG_SECONDS"

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

	// Network defaults
	DefaultNetworkInitialBackoffSeconds = 1
	DefaultNetworkMaxBackoffSeconds     = 30
	DefaultNetworkBackoffJitter         = 0.2

	// Timeout defaults
	DefaultTimeoutPublishSeconds   = 10
	DefaultTimeoutSubscribeSeconds = 10
)
