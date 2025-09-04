package main

import (
	"context"
	"event-forwarder/pkg/config"
	"event-forwarder/pkg/forwarder"
	"event-forwarder/pkg/telemetry"
	"event-forwarder/pkg/version"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/rivo/tview"
)

func main() {
	// Check for version flag first
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		info := version.Info()
		fmt.Printf("fwd version %s, commit %s, built %s\n", info.Version, info.Commit, info.Built)
		return
	}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	if cfg == nil {
		fmt.Fprintf(os.Stderr, "Configuration is nil\n")
		os.Exit(1)
	}

	// Create telemetry system
	telemetryConfig := telemetry.DefaultConfig()
	aggregator := telemetry.NewAggregator(telemetry.RealClock{}, telemetryConfig)

	// Create logger that doesn't interfere with TUI
	logger := log.New(os.Stderr, "", 0)

	// Create forwarder with telemetry
	fwd := forwarder.New(cfg, logger, aggregator)

	// Create context for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start telemetry aggregator
	aggregator.Start(ctx)
	defer aggregator.Stop()

	// Start TUI
	tui := NewTUI(aggregator, cfg)
	
	// Start forwarder in background
	go func() {
		if err := fwd.Start(ctx); err != nil && err != context.Canceled {
			tui.SetError(fmt.Sprintf("Forwarder error: %v", err))
		}
	}()

	// Run TUI (blocking)
	if err := tui.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}

// TUI represents the terminal user interface
type TUI struct {
	app       *tview.Application
	telemetry telemetry.TelemetryReader
	config    *config.Config

	// UI components
	statusText    *tview.TextView
	relayTable    *tview.Table
	statsTable    *tview.Table
	kindTable     *tview.Table
	errorsList    *tview.TextView
	currentWindow *tview.TextView

	// State
	lastError string
}

// NewTUI creates a new terminal user interface
func NewTUI(telemetryReader telemetry.TelemetryReader, cfg *config.Config) *TUI {
	tui := &TUI{
		app:       tview.NewApplication(),
		telemetry: telemetryReader,
		config:    cfg,
	}

	tui.setupUI()
	return tui
}

func (t *TUI) setupUI() {
	// Create components
	t.statusText = tview.NewTextView().
		SetDynamicColors(true).
		SetText("[green]● [white]Starting...")
	t.statusText.SetBorder(true).SetTitle(" Status ")

	t.relayTable = tview.NewTable().SetBorders(false)
	t.relayTable.SetBorder(true).SetTitle(" Relays ")

	t.statsTable = tview.NewTable().SetBorders(false)
	t.statsTable.SetBorder(true).SetTitle(" Event Stats ")

	t.kindTable = tview.NewTable().SetBorders(false)
	t.kindTable.SetBorder(true).SetTitle(" Event Types ")

	t.errorsList = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	t.errorsList.SetBorder(true).SetTitle(" Recent Errors ")

	t.currentWindow = tview.NewTextView().
		SetDynamicColors(true)
	t.currentWindow.SetBorder(true).SetTitle(" Current Window ")

	// Setup layout
	topRow := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(t.statusText, 0, 1, false).
		AddItem(t.currentWindow, 0, 1, false)

	middleRow := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(t.relayTable, 0, 1, false).
		AddItem(t.statsTable, 0, 1, false)

	bottomRow := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(t.kindTable, 0, 1, false).
		AddItem(t.errorsList, 0, 2, false)

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(topRow, 5, 0, false).
		AddItem(middleRow, 8, 0, false).
		AddItem(bottomRow, 0, 1, false)

	// Add header
	header := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetDynamicColors(true).
		SetText(fmt.Sprintf("[yellow]DeepFry Event Forwarder v%s[white] - Press Ctrl+C to quit", version.Info().Version))

	main := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 1, 0, false).
		AddItem(layout, 0, 1, false)

	t.app.SetRoot(main, true)

	// Start update ticker
	go t.updateLoop()
}

func (t *TUI) updateLoop() {
	ticker := time.NewTicker(200 * time.Millisecond) // 5 FPS
	defer ticker.Stop()

	for range ticker.C {
		t.app.QueueUpdateDraw(func() {
			t.updateDisplay()
		})
	}
}

func (t *TUI) updateDisplay() {
	snapshot := t.telemetry.Snapshot()

	// Update status
	status := "[green]● RUNNING"
	if t.lastError != "" {
		status = "[red]● ERROR"
	}
	uptime := time.Duration(snapshot.UptimeSeconds * float64(time.Second))
	t.statusText.SetText(fmt.Sprintf("%s\nUptime: %v\nLast Event: %.1fs ago",
		status, uptime.Truncate(time.Second), time.Now().Sub(time.Unix(snapshot.SyncWindowTo, 0)).Seconds()))

	// Update current window
	if snapshot.SyncWindowFrom > 0 && snapshot.SyncWindowTo > 0 {
		from := time.Unix(snapshot.SyncWindowFrom, 0).Format("15:04:05")
		to := time.Unix(snapshot.SyncWindowTo, 0).Format("15:04:05")
		t.currentWindow.SetText(fmt.Sprintf("From: %s\nTo: %s\nLag: %.1fs",
			from, to, snapshot.SyncLagSeconds))
	}

	// Update relay status
	t.relayTable.Clear()
	t.relayTable.SetCell(0, 0, tview.NewTableCell("Source:").SetTextColor(tview.Styles.SecondaryTextColor))
	sourceStatus := "[red]●DISC"
	if snapshot.SourceRelayConnected {
		sourceStatus = "[green]●CONN"
	}
	t.relayTable.SetCell(0, 1, tview.NewTableCell(sourceStatus))
	
	t.relayTable.SetCell(1, 0, tview.NewTableCell("DeepFry:").SetTextColor(tview.Styles.SecondaryTextColor))
	deepfryStatus := "[red]●DISC"
	if snapshot.DeepFryRelayConnected {
		deepfryStatus = "[green]●CONN"
	}
	t.relayTable.SetCell(1, 1, tview.NewTableCell(deepfryStatus))

	// Update stats
	t.statsTable.Clear()
	t.statsTable.SetCell(0, 0, tview.NewTableCell("Received:").SetTextColor(tview.Styles.SecondaryTextColor))
	t.statsTable.SetCell(0, 1, tview.NewTableCell(fmt.Sprintf("%s (%.1f/s)", 
		formatNumber(snapshot.EventsReceived), snapshot.EventsPerSecond)))

	t.statsTable.SetCell(1, 0, tview.NewTableCell("Forwarded:").SetTextColor(tview.Styles.SecondaryTextColor))
	t.statsTable.SetCell(1, 1, tview.NewTableCell(fmt.Sprintf("%s (%.1f/s)", 
		formatNumber(snapshot.EventsForwarded), snapshot.ForwardsPerSecond)))

	t.statsTable.SetCell(2, 0, tview.NewTableCell("Errors:").SetTextColor(tview.Styles.SecondaryTextColor))
	errorRate := 0.0
	if snapshot.EventsReceived > 0 {
		errorRate = float64(snapshot.ErrorsTotal) / float64(snapshot.EventsReceived) * 100
	}
	t.statsTable.SetCell(2, 1, tview.NewTableCell(fmt.Sprintf("%s (%.2f%%)", 
		formatNumber(snapshot.ErrorsTotal), errorRate)))

	t.statsTable.SetCell(3, 0, tview.NewTableCell("Queue:").SetTextColor(tview.Styles.SecondaryTextColor))
	t.statsTable.SetCell(3, 1, tview.NewTableCell(fmt.Sprintf("%.0f%%", snapshot.ChannelUtilization)))

	// Update event kinds
	t.kindTable.Clear()
	row := 0
	for kind, count := range snapshot.EventsForwardedByKind {
		if row >= 10 { // Limit display
			break
		}
		kindName := getKindName(kind)
		t.kindTable.SetCell(row, 0, tview.NewTableCell(kindName).SetTextColor(tview.Styles.SecondaryTextColor))
		t.kindTable.SetCell(row, 1, tview.NewTableCell(formatNumber(count)))
		row++
	}

	// Update recent errors
	errorText := ""
	for i, err := range snapshot.RecentErrors {
		if i >= 10 { // Show last 10 errors
			break
		}
		errorText += fmt.Sprintf("[yellow]• [white]%s\n", err)
	}
	if errorText == "" {
		errorText = "[green]No recent errors"
	}
	t.errorsList.SetText(errorText)

	// Set error if we have one
	if t.lastError != "" {
		t.statusText.SetText(fmt.Sprintf("[red]● ERROR\n%s", t.lastError))
	}
}

func (t *TUI) SetError(err string) {
	t.lastError = err
}

func (t *TUI) Run() error {
	return t.app.Run()
}

func formatNumber(n uint64) string {
	str := strconv.FormatUint(n, 10)
	if len(str) <= 3 {
		return str
	}
	
	result := ""
	for i, c := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result += ","
		}
		result += string(c)
	}
	return result
}

func getKindName(kind int) string {
	switch kind {
	case 0:
		return "Metadata"
	case 1:
		return "Text Note"
	case 2:
		return "Relay List"
	case 3:
		return "Contacts"
	case 4:
		return "DM"
	case 5:
		return "Event Delete"
	case 6:
		return "Repost"
	case 7:
		return "Reaction"
	case 8:
		return "Badge Award"
	default:
		return fmt.Sprintf("Kind %d", kind)
	}
}
