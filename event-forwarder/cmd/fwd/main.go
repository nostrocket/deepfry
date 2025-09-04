package main

import (
	"context"
	"event-forwarder/pkg/config"
	"event-forwarder/pkg/forwarder"
	"event-forwarder/pkg/telemetry"
	"event-forwarder/pkg/utils"
	"event-forwarder/pkg/version"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"
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
		// Help was shown, exit gracefully
		os.Exit(0)
	}

	// Create telemetry system
	telemetryConfig := telemetry.DefaultConfig()
	aggregator := telemetry.NewAggregator(telemetry.RealClock{}, telemetryConfig)

	// Create a silent logger to avoid interfering with TUI
	// All logging is handled through telemetry instead
	logger := log.New(io.Discard, "", 0)

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

	// Start forwarder in background after TUI is set up
	go func() {
		// Small delay to let TUI initialize completely
		time.Sleep(100 * time.Millisecond)
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
	progressBar   *tview.TextView
	timelineView  *tview.TextView

	// State
	lastError        string
	ready            bool
	syncStartTime    time.Time // Configured sync start time
	syncStartTimeSet bool      // Whether start time has been determined
	targetEndTime    time.Time // Current time (target end)

	// Control channels
	done chan struct{}
}

// NewTUI creates a new terminal user interface
func NewTUI(telemetryReader telemetry.TelemetryReader, cfg *config.Config) *TUI {
	tui := &TUI{
		app:       tview.NewApplication(),
		telemetry: telemetryReader,
		config:    cfg,
		done:      make(chan struct{}),
	}

	// Parse sync start time if configured
	if startTime, err := cfg.Sync.GetStartTime(); err == nil && !startTime.IsZero() {
		tui.syncStartTime = startTime
		tui.syncStartTimeSet = true
	}
	// If no start time configured, we'll set it dynamically from first SyncWindowFrom
	tui.targetEndTime = time.Now()

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
		SetScrollable(false). // Disable scrolling to prevent bouncing
		SetWrap(false)        // Disable text wrapping
	t.errorsList.SetBorder(true).SetTitle(" Recent Errors ")

	t.currentWindow = tview.NewTextView().
		SetDynamicColors(true)
	t.currentWindow.SetBorder(true).SetTitle(" Current Window ")

	// Create progress bar for sync timeline
	t.progressBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	t.progressBar.SetBorder(true).SetTitle(" Sync Progress ")

	// Create timeline view showing key dates
	t.timelineView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	t.timelineView.SetBorder(true).SetTitle(" Timeline ")

	// Setup layout with new progress components
	topRow := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(t.statusText, 0, 1, false).
		AddItem(t.currentWindow, 0, 1, false)

	// Add progress row
	progressRow := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(t.progressBar, 0, 2, false).
		AddItem(t.timelineView, 0, 1, false)

	middleRow := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(t.relayTable, 0, 1, false).
		AddItem(t.statsTable, 0, 1, false)

	bottomRow := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(t.kindTable, 0, 1, false).
		AddItem(t.errorsList, 0, 2, false)

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(topRow, 5, 0, false).
		AddItem(progressRow, 4, 0, false).
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

	// Mark as ready and start update ticker
	t.ready = true
	go t.updateLoop()
}

func (t *TUI) updateLoop() {
	ticker := time.NewTicker(1000 * time.Millisecond) // Slower: 1 FPS
	defer ticker.Stop()

	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
			// Only update if the app is ready and still running
			if t.ready && t.app != nil {
				t.app.QueueUpdateDraw(func() {
					t.updateDisplay()
				})
			}
		}
	}
}

func (t *TUI) updateDisplay() {
	// Get snapshot once to avoid multiple calls during update
	snapshot := t.telemetry.Snapshot()

	// Update target end time to current time
	t.targetEndTime = time.Now()

	// Update status
	status := "[green]● RUNNING"
	if t.lastError != "" {
		status = "[red]● ERROR"
	}
	uptime := time.Duration(snapshot.UptimeSeconds * float64(time.Second))

	// Handle case where SyncWindowTo might be 0
	var lastEventText string
	if snapshot.SyncWindowTo > 0 {
		lastEventTime := time.Unix(snapshot.SyncWindowTo, 0)
		lastEventText = fmt.Sprintf("%.1fs ago", time.Since(lastEventTime).Seconds())
	} else {
		lastEventText = "Never"
	}

	t.statusText.SetText(fmt.Sprintf("%s\nUptime: %v\nLast Event: %s",
		status, uptime.Truncate(time.Second), lastEventText))

	// Update current window
	if snapshot.SyncWindowFrom > 0 && snapshot.SyncWindowTo > 0 {
		from := time.Unix(snapshot.SyncWindowFrom, 0).Format("15:04:05")
		to := time.Unix(snapshot.SyncWindowTo, 0).Format("15:04:05")
		t.currentWindow.SetText(fmt.Sprintf("From: %s\nTo: %s\nLag: %.1fs",
			from, to, snapshot.SyncLagSeconds))
	}

	// Update progress bar and timeline
	t.updateProgressDisplay(snapshot)

	// Update relay status
	t.relayTable.Clear()
	t.relayTable.SetCell(0, 0, tview.NewTableCell("Source:").SetTextColor(tview.Styles.SecondaryTextColor))
	sourceStatus := "[red]✘ DISC"
	if snapshot.SourceRelayConnected {
		sourceStatus = "[green]✓ CONNECTED"
	}
	t.relayTable.SetCell(0, 1, tview.NewTableCell(sourceStatus))

	t.relayTable.SetCell(1, 0, tview.NewTableCell("DeepFry:").SetTextColor(tview.Styles.SecondaryTextColor))
	deepfryStatus := "[red]✘ DISC"
	if snapshot.DeepFryRelayConnected {
		deepfryStatus = "[green]✓ CONNECTED"
	}
	t.relayTable.SetCell(1, 1, tview.NewTableCell(deepfryStatus))

	// Update stats
	t.statsTable.Clear()
	t.statsTable.SetCell(0, 0, tview.NewTableCell("Received:").SetTextColor(tview.Styles.SecondaryTextColor))
	t.statsTable.SetCell(0, 1, tview.NewTableCell(fmt.Sprintf("%s (%.1f/s)",
		utils.FormatNumber(snapshot.EventsReceived), snapshot.EventsPerSecond))) // Use utils.FormatNumber

	t.statsTable.SetCell(1, 0, tview.NewTableCell("Forwarded:").SetTextColor(tview.Styles.SecondaryTextColor))
	t.statsTable.SetCell(1, 1, tview.NewTableCell(fmt.Sprintf("%s (%.1f/s)",
		utils.FormatNumber(snapshot.EventsForwarded), snapshot.ForwardsPerSecond))) // Use utils.FormatNumber

	t.statsTable.SetCell(2, 0, tview.NewTableCell("Errors:").SetTextColor(tview.Styles.SecondaryTextColor))
	errorRate := 0.0
	if snapshot.EventsReceived > 0 {
		errorRate = float64(snapshot.ErrorsTotal) / float64(snapshot.EventsReceived) * 100
	}
	t.statsTable.SetCell(2, 1, tview.NewTableCell(fmt.Sprintf("%s (%.2f%%)",
		utils.FormatNumber(snapshot.ErrorsTotal), errorRate))) // Use utils.FormatNumber

	t.statsTable.SetCell(3, 0, tview.NewTableCell("Queue:").SetTextColor(tview.Styles.SecondaryTextColor))
	t.statsTable.SetCell(3, 1, tview.NewTableCell(fmt.Sprintf("%.0f%%", snapshot.ChannelUtilization)))

	// Update event kinds
	t.kindTable.Clear()

	// Use the utility function to get sorted kind counts
	sortedKinds := utils.SortEventKindsByCount(snapshot.EventsForwardedByKind)

	for i, kc := range sortedKinds {
		kindName := utils.GetKindName(kc.Kind)
		t.kindTable.SetCell(i, 0, tview.NewTableCell(kindName).SetTextColor(tview.Styles.SecondaryTextColor))
		t.kindTable.SetCell(i, 1, tview.NewTableCell(utils.FormatNumber(kc.Count)))
	}

	// Update recent errors
	errorText := ""
	maxErrors := 8 // Reduce to prevent scrolling
	for i, err := range snapshot.RecentErrors {
		if i >= maxErrors {
			break
		}
		errorText += fmt.Sprintf("[yellow]✘ [white]%s\n", err)
	}
	if errorText == "" {
		errorText = "[green]No recent errors"
	}

	// Only update if text has changed to reduce flicker
	if t.errorsList.GetText(false) != errorText {
		t.errorsList.SetText(errorText)
	}

	// Set error if we have one
	if t.lastError != "" {
		t.statusText.SetText(fmt.Sprintf("[red]● ERROR\n%s", t.lastError))
	}
}

func (t *TUI) updateProgressDisplay(snapshot telemetry.Snapshot) {
	// Set start time from first SyncWindowFrom if not already configured
	if !t.syncStartTimeSet && snapshot.SyncWindowFrom > 0 {
		t.syncStartTime = time.Unix(snapshot.SyncWindowFrom, 0)
		t.syncStartTimeSet = true
	}

	// Calculate progress based on sync timeline
	totalDuration := t.targetEndTime.Sub(t.syncStartTime)

	var currentPosition time.Time
	var progressPercent float64

	if snapshot.SyncWindowTo > 0 {
		currentPosition = time.Unix(snapshot.SyncWindowTo, 0)
		if t.syncStartTimeSet {
			elapsed := currentPosition.Sub(t.syncStartTime)
			if totalDuration > 0 {
				progressPercent = float64(elapsed) / float64(totalDuration) * 100
				if progressPercent > 100 {
					progressPercent = 100
				}
			}
		}
	}

	// Create visual progress bar
	barWidth := 50
	filledWidth := int(float64(barWidth) * progressPercent / 100)
	if filledWidth > barWidth {
		filledWidth = barWidth
	}

	progressBar := ""
	for i := 0; i < barWidth; i++ {
		if i < filledWidth {
			progressBar += "█"
		} else {
			progressBar += "░"
		}
	}

	// Color the progress bar based on status
	progressColor := "green"
	if progressPercent < 10 {
		progressColor = "red"
	} else if progressPercent < 50 {
		progressColor = "yellow"
	}

	progressText := fmt.Sprintf("[%s]%s[white] %.1f%%\n", progressColor, progressBar, progressPercent)

	// Add current sync position info
	if !currentPosition.IsZero() {
		progressText += fmt.Sprintf("Position: %s\n", currentPosition.Format("2006-01-02 15:04:05"))
		remaining := t.targetEndTime.Sub(currentPosition)
		if remaining > 0 {
			progressText += fmt.Sprintf("Remaining: %v", remaining.Truncate(time.Second))
		} else {
			progressText += "[green]✓ Caught up!"
		}
	} else {
		progressText += "Waiting for sync to start..."
	}

	t.progressBar.SetText(progressText)

	// Update timeline view
	var timelineText string
	if t.syncStartTimeSet {
		timelineText = fmt.Sprintf("Start: %s\n", t.syncStartTime.Format("2006-01-02 15:04"))
		timelineText += fmt.Sprintf("Target: %s\n", t.targetEndTime.Format("2006-01-02 15:04"))

		if !currentPosition.IsZero() {
			timelineText += fmt.Sprintf("Current: %s", currentPosition.Format("2006-01-02 15:04"))
		}
	} else {
		timelineText = "Waiting for sync data...\n"
		timelineText += fmt.Sprintf("Target: %s", t.targetEndTime.Format("2006-01-02 15:04"))
	}

	t.timelineView.SetText(timelineText)
}

func (t *TUI) SetError(err string) {
	t.lastError = err
}

func (t *TUI) Run() error {
	// Set up input capture first, before starting the app
	t.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlC:
			t.app.Stop()
			return nil
		case tcell.KeyEscape:
			t.app.Stop()
			return nil
		default:
			// Consume all other input to prevent it from being printed
			return nil
		}
	})

	// Ensure we properly close the done channel
	defer func() {
		close(t.done)
	}()

	return t.app.Run()
}
