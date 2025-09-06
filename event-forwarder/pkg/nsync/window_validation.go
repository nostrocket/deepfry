package nsync

import (
	"fmt"
	"time"
)

// WindowValidationError represents validation errors for Window operations
type WindowValidationError struct {
	Field   string
	Value   interface{}
	Message string
}

func (e WindowValidationError) Error() string {
	return fmt.Sprintf("window validation error: %s (%v): %s", e.Field, e.Value, e.Message)
}

// Validate performs comprehensive validation on a Window
func (w Window) Validate() error {
	// Check for zero time values
	if w.From.IsZero() {
		return WindowValidationError{
			Field:   "From",
			Value:   w.From,
			Message: "window start time cannot be zero",
		}
	}

	if w.To.IsZero() {
		return WindowValidationError{
			Field:   "To",
			Value:   w.To,
			Message: "window end time cannot be zero",
		}
	}

	// Check for logical time ordering
	if !w.From.Before(w.To) {
		return WindowValidationError{
			Field:   "From/To",
			Value:   fmt.Sprintf("From=%v, To=%v", w.From, w.To),
			Message: "window start time must be before end time",
		}
	}

	// Check for reasonable duration bounds (not too small, not too large)
	duration := w.To.Sub(w.From)
	if duration < time.Second {
		return WindowValidationError{
			Field:   "Duration",
			Value:   duration,
			Message: "window duration cannot be less than 1 second",
		}
	}

	// Prevent extremely large windows (more than 1 year)
	maxDuration := 365 * 24 * time.Hour
	if duration > maxDuration {
		return WindowValidationError{
			Field:   "Duration",
			Value:   duration,
			Message: "window duration cannot exceed 1 year",
		}
	}

	return nil
}

// ValidateDuration checks if a duration is suitable for window operations
func ValidateDuration(duration time.Duration) error {
	if duration <= 0 {
		return WindowValidationError{
			Field:   "Duration",
			Value:   duration,
			Message: "duration must be positive",
		}
	}

	if duration < time.Second {
		return WindowValidationError{
			Field:   "Duration",
			Value:   duration,
			Message: "duration cannot be less than 1 second",
		}
	}

	// Prevent extremely large durations
	maxDuration := 365 * 24 * time.Hour
	if duration > maxDuration {
		return WindowValidationError{
			Field:   "Duration",
			Value:   duration,
			Message: "duration cannot exceed 1 year",
		}
	}

	return nil
}

// ValidateTimestamp checks if a timestamp is reasonable for window operations
func ValidateTimestamp(t time.Time) error {
	if t.IsZero() {
		return WindowValidationError{
			Field:   "Timestamp",
			Value:   t,
			Message: "timestamp cannot be zero",
		}
	}

	// Check for reasonable time bounds (not too far in past/future)
	now := time.Now()
	minTime := now.AddDate(-10, 0, 0) // 10 years ago
	maxTime := now.AddDate(1, 0, 0)   // 1 year in future

	if t.Before(minTime) {
		return WindowValidationError{
			Field:   "Timestamp",
			Value:   t,
			Message: "timestamp cannot be more than 10 years in the past",
		}
	}

	if t.After(maxTime) {
		return WindowValidationError{
			Field:   "Timestamp",
			Value:   t,
			Message: "timestamp cannot be more than 1 year in the future",
		}
	}

	return nil
}

// SafeNewWindow creates a new window with validation
func SafeNewWindow(duration time.Duration) (Window, error) {
	if err := ValidateDuration(duration); err != nil {
		return Window{}, err
	}

	window := NewWindow(duration)
	if err := window.Validate(); err != nil {
		return Window{}, err
	}

	return window, nil
}

// SafeNewWindowFromStart creates a new window from start time with validation
func SafeNewWindowFromStart(startTime time.Time, duration time.Duration) (Window, error) {
	if err := ValidateTimestamp(startTime); err != nil {
		return Window{}, err
	}

	if err := ValidateDuration(duration); err != nil {
		return Window{}, err
	}

	window := NewWindowFromStart(startTime, duration)
	if err := window.Validate(); err != nil {
		return Window{}, err
	}

	return window, nil
}

// SafeNext advances the window with validation
func (w Window) SafeNext(duration time.Duration) (Window, error) {
	if err := w.Validate(); err != nil {
		return Window{}, fmt.Errorf("invalid source window: %w", err)
	}

	if err := ValidateDuration(duration); err != nil {
		return Window{}, err
	}

	nextWindow := w.Next(duration)
	if err := nextWindow.Validate(); err != nil {
		return Window{}, fmt.Errorf("invalid resulting window: %w", err)
	}

	return nextWindow, nil
}
