package nsync

import (
	"math/rand"
	"testing"
	"testing/quick"
	"time"
)

// Property-based testing for window operations
func TestWindowProperties(t *testing.T) {
	t.Run("Window validation properties", func(t *testing.T) {
		config := &quick.Config{
			MaxCount: 100,
		}

		// Property: Valid windows should always pass validation
		validWindowProperty := func(durationSeconds int64) bool {
			if durationSeconds <= 0 || durationSeconds > 365*24*3600 {
				return true // Skip invalid inputs
			}

			duration := time.Duration(durationSeconds) * time.Second
			window := NewWindow(duration)
			return window.Validate() == nil
		}

		if err := quick.Check(validWindowProperty, config); err != nil {
			t.Errorf("Valid window property failed: %v", err)
		}

		// Property: Window.Next should preserve duration
		nextPreservesDurationProperty := func(durationSeconds int64) bool {
			if durationSeconds <= 0 || durationSeconds > 365*24*3600 {
				return true // Skip invalid inputs
			}

			duration := time.Duration(durationSeconds) * time.Second
			window := NewWindow(duration)
			nextWindow := window.Next(duration)

			originalDuration := window.To.Sub(window.From)
			nextDuration := nextWindow.To.Sub(nextWindow.From)

			return originalDuration == nextDuration
		}

		if err := quick.Check(nextPreservesDurationProperty, config); err != nil {
			t.Errorf("Next preserves duration property failed: %v", err)
		}

		// Property: Window.Next should have contiguous time ranges
		nextContiguousProperty := func(durationSeconds int64) bool {
			if durationSeconds <= 0 || durationSeconds > 365*24*3600 {
				return true // Skip invalid inputs
			}

			duration := time.Duration(durationSeconds) * time.Second
			window := NewWindow(duration)
			nextWindow := window.Next(duration)

			return window.To.Equal(nextWindow.From)
		}

		if err := quick.Check(nextContiguousProperty, config); err != nil {
			t.Errorf("Next contiguous property failed: %v", err)
		}
	})

	t.Run("Duration validation properties", func(t *testing.T) {
		config := &quick.Config{
			MaxCount: 50,
		}

		// Property: Positive durations within bounds should be valid
		validDurationProperty := func(seconds int64) bool {
			if seconds <= 0 || seconds > 365*24*3600 {
				// Should be invalid
				duration := time.Duration(seconds) * time.Second
				return ValidateDuration(duration) != nil
			} else {
				// Should be valid
				duration := time.Duration(seconds) * time.Second
				return ValidateDuration(duration) == nil
			}
		}

		if err := quick.Check(validDurationProperty, config); err != nil {
			t.Errorf("Valid duration property failed: %v", err)
		}
	})
}

// Boundary condition testing
func TestWindowBoundaryConditions(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func() (Window, error)
		wantErr   bool
		errType   string
	}{
		{
			name: "minimum valid duration",
			setupFunc: func() (Window, error) {
				return SafeNewWindow(time.Second)
			},
			wantErr: false,
		},
		{
			name: "sub-second duration",
			setupFunc: func() (Window, error) {
				return SafeNewWindow(500 * time.Millisecond)
			},
			wantErr: true,
			errType: "Duration",
		},
		{
			name: "zero duration",
			setupFunc: func() (Window, error) {
				return SafeNewWindow(0)
			},
			wantErr: true,
			errType: "Duration",
		},
		{
			name: "negative duration",
			setupFunc: func() (Window, error) {
				return SafeNewWindow(-time.Hour)
			},
			wantErr: true,
			errType: "Duration",
		},
		{
			name: "maximum valid duration",
			setupFunc: func() (Window, error) {
				return SafeNewWindow(365 * 24 * time.Hour)
			},
			wantErr: false,
		},
		{
			name: "excessive duration",
			setupFunc: func() (Window, error) {
				return SafeNewWindow(366 * 24 * time.Hour)
			},
			wantErr: true,
			errType: "Duration",
		},
		{
			name: "zero start time",
			setupFunc: func() (Window, error) {
				return SafeNewWindowFromStart(time.Time{}, time.Hour)
			},
			wantErr: true,
			errType: "Timestamp",
		},
		{
			name: "far past start time",
			setupFunc: func() (Window, error) {
				farPast := time.Now().AddDate(-15, 0, 0)
				return SafeNewWindowFromStart(farPast, time.Hour)
			},
			wantErr: true,
			errType: "Timestamp",
		},
		{
			name: "far future start time",
			setupFunc: func() (Window, error) {
				farFuture := time.Now().AddDate(2, 0, 0)
				return SafeNewWindowFromStart(farFuture, time.Hour)
			},
			wantErr: true,
			errType: "Timestamp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			window, err := tt.setupFunc()

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}

				if tt.errType != "" {
					if validationErr, ok := err.(WindowValidationError); ok {
						if validationErr.Field != tt.errType {
							t.Errorf("expected error type %s, got %s", tt.errType, validationErr.Field)
						}
					} else {
						t.Errorf("expected WindowValidationError, got %T", err)
					}
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}

				// Validate the resulting window
				if err := window.Validate(); err != nil {
					t.Errorf("window validation failed: %v", err)
				}
			}
		})
	}
}

// Test window sequence generation and validation
func TestWindowSequenceValidation(t *testing.T) {
	duration := time.Hour
	initialWindow := NewWindow(duration)

	var windows []Window
	windows = append(windows, initialWindow)

	// Generate a sequence of windows
	for i := 0; i < 10; i++ {
		nextWindow, err := windows[len(windows)-1].SafeNext(duration)
		if err != nil {
			t.Fatalf("failed to generate next window at step %d: %v", i, err)
		}
		windows = append(windows, nextWindow)
	}

	// Validate sequence properties
	for i := 1; i < len(windows); i++ {
		prev := windows[i-1]
		curr := windows[i]

		// Check contiguity
		if !prev.To.Equal(curr.From) {
			t.Errorf("windows %d and %d are not contiguous: prev.To=%v, curr.From=%v",
				i-1, i, prev.To, curr.From)
		}

		// Check duration consistency
		prevDuration := prev.To.Sub(prev.From)
		currDuration := curr.To.Sub(curr.From)
		if prevDuration != currDuration {
			t.Errorf("window %d has inconsistent duration: expected %v, got %v",
				i, prevDuration, currDuration)
		}

		// Check individual window validity
		if err := curr.Validate(); err != nil {
			t.Errorf("window %d failed validation: %v", i, err)
		}
	}
}

// Fuzzing-style test for edge cases
func TestWindowFuzzValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fuzz test in short mode")
	}

	// Generate random test cases
	rand.Seed(time.Now().UnixNano())

	for i := 0; i < 1000; i++ {
		// Random duration between 1 nanosecond and 2 years
		durationNanos := rand.Int63n(2 * 365 * 24 * 3600 * 1e9)
		duration := time.Duration(durationNanos)

		// Random start time within reasonable bounds
		now := time.Now()
		minTime := now.AddDate(-5, 0, 0)
		maxTime := now.AddDate(1, 0, 0)
		timeRange := maxTime.Sub(minTime)
		randomOffset := time.Duration(rand.Int63n(int64(timeRange)))
		startTime := minTime.Add(randomOffset)

		// Test NewWindowFromStart with validation
		window, err := SafeNewWindowFromStart(startTime, duration)

		// If we got a window, it should be valid
		if err == nil {
			if validationErr := window.Validate(); validationErr != nil {
				t.Errorf("iteration %d: SafeNewWindowFromStart returned invalid window: %v", i, validationErr)
			}

			// Test Next operation
			if nextWindow, nextErr := window.SafeNext(duration); nextErr == nil {
				if nextValidationErr := nextWindow.Validate(); nextValidationErr != nil {
					t.Errorf("iteration %d: SafeNext returned invalid window: %v", i, nextValidationErr)
				}
			}
		}
	}
}

// Performance test for validation overhead
func BenchmarkWindowValidation(b *testing.B) {
	window := NewWindow(time.Hour)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = window.Validate()
	}
}

func BenchmarkSafeWindowCreation(b *testing.B) {
	duration := time.Hour

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = SafeNewWindow(duration)
	}
}

func BenchmarkSafeNext(b *testing.B) {
	window := NewWindow(time.Hour)
	duration := time.Hour

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = window.SafeNext(duration)
	}
}
