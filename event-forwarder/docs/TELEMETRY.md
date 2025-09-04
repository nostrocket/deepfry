# Telemetry & TUI Implementation

## Overview

The DeepFry Event Forwarder now includes a comprehensive telemetry system and Terminal User Interface (TUI) for real-time monitoring.

## Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Forwarder     │───▶│   Aggregator    │───▶│      TUI        │
│   (Publisher)   │    │  (Processor)    │    │   (Display)     │
└─────────────────┘    └─────────────────┘    └─────────────────┘
         │                       │                       │
         ▼                       ▼                       ▼
   Telemetry Events      Buffered Channel         Real-time UI
```

## Components

### 1. TelemetryPublisher Interface
- **Purpose**: Event publishing from forwarder to aggregator
- **Implementation**: Non-blocking, fire-and-forget pattern
- **Events**: EventReceived, EventForwarded, ConnectionStatusChanged, ForwarderError, SyncProgressUpdated

### 2. Aggregator 
- **Purpose**: Stateful processing of telemetry events
- **Features**: 
  - Thread-safe metric collection
  - Rate calculations (events/second)
  - Latency tracking (avg, P95)
  - Error categorization by type and severity
  - Recent error ring buffer
- **Performance**: Protected hot path with buffered channels

### 3. TelemetryReader Interface
- **Purpose**: Snapshot-based data access for consumers
- **Features**: 
  - Immutable snapshots prevent data races
  - Real-time metrics calculation
  - Connection status tracking
  - Sync window progress monitoring

### 4. TUI (Terminal User Interface)
- **Framework**: `github.com/rivo/tview`
- **Layout**: HTOP-style dashboard with multiple panels
- **Refresh Rate**: 200ms (5 FPS)
- **Features**:
  - Real-time event statistics
  - Connection status indicators
  - Event type breakdown
  - Recent error display
  - Sync progress monitoring

## Usage

### Starting the Application
```bash
./fwd
```

The TUI will automatically start and display real-time metrics.

### Key Metrics Displayed

1. **Status Panel**
   - Running status (●RUNNING/●ERROR)
   - Uptime
   - Time since last event

2. **Relay Panel** 
   - Source relay: ●CONN/●DISC
   - DeepFry relay: ●CONN/●DISC

3. **Event Stats Panel**
   - Events received (total + rate/sec)
   - Events forwarded (total + rate/sec) 
   - Error count and percentage
   - Queue utilization

4. **Event Types Panel**
   - Breakdown by Nostr event kind
   - Text Notes, Reactions, Reposts, etc.

5. **Current Window Panel**
   - Sync window timeframe
   - Lag behind real-time

6. **Recent Errors Panel**
   - Last 10 errors with context
   - Color-coded by severity

## Testing

### Unit Tests
```bash
go test ./pkg/telemetry/ -v    # Telemetry package tests
go test ./pkg/forwarder/ -v    # Forwarder integration tests
```

### Integration Tests  
```bash
go test -tags=integration ./pkg/forwarder/ -v
```

## Performance Characteristics

- **Channel Buffer**: 1000 events (configurable)
- **Drop Policy**: Events dropped when buffer full (protects hot path)
- **Memory Usage**: Ring buffers for errors (50 max) and latencies (100 max)
- **CPU Overhead**: Minimal - async processing with efficient data structures
- **Refresh Rate**: 200ms TUI updates, non-blocking aggregation

## Configuration

```go
type Config struct {
    BufferSize          int     `default:"1000"`
    DropThresholdPercent float64 `default:"90.0"`
    RefreshIntervalMs   int     `default:"200"`
    MaxRecentErrors     int     `default:"50"`
    RateWindowSeconds   int     `default:"10"`
}
```

## Future Extensions

The interface-based design allows easy addition of:
- Prometheus metrics exporter
- JSON API endpoint
- Alert thresholds
- Historical data persistence
- Custom dashboards

## Exit

Press `Ctrl+C` to gracefully shutdown the application.
