package telemetry

type Snapshot struct {
	// Core metrics
	EventsReceived        uint64
	EventsForwarded       uint64
	ErrorsTotal           uint64
	EventsForwardedByKind map[int]uint64
	
	// Sync state
	SyncLagSeconds        float64
	SyncWindowFrom        int64
	SyncWindowTo          int64
	
	// Connection status
	SourceRelayConnected  bool
	DeepFryRelayConnected bool
	
	// Rate metrics
	EventsPerSecond       float64
	ForwardsPerSecond     float64
	
	// Latency metrics
	AvgLatencyMs          float64
	P95LatencyMs          float64
	
	// System metrics
	UptimeSeconds         float64
	ChannelUtilization    float64
	
	// Error breakdown
	ErrorsByType          map[string]uint64
	ErrorsBySeverity      map[ErrorSeverity]uint64
	RecentErrors          []string
}

type TelemetryReader interface {
	Snapshot() Snapshot
}
