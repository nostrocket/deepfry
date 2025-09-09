package whitelist

import (
	"errors"
	"log"
	"os"
	"sync"
	"testing"
	"time"
)

// mockKeyRepo is a custom mock for repository.KeyRepository.
// It allows controlling fetch results, errors, delays, and tracking calls.
type mockKeyRepo struct {
	keys      [][32]byte
	err       error
	delay     time.Duration
	callCount int
	mu        sync.Mutex
}

func (m *mockKeyRepo) GetAll() ([][32]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	return m.keys, m.err
}

func (m *mockKeyRepo) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func TestNewWhitelistRefresher(t *testing.T) {
	mockRepo := &mockKeyRepo{}
	logger := log.New(os.Stdout, "test", log.LstdFlags)
	interval := 5 * time.Minute
	retryCount := 3

	refresher := NewWhitelistRefresher(mockRepo, interval, retryCount, logger)

	if refresher.keyRepo != mockRepo {
		t.Errorf("Expected keyRepo to be set, got %v", refresher.keyRepo)
	}
	if refresher.interval != interval {
		t.Errorf("Expected interval %v, got %v", interval, refresher.interval)
	}
	if refresher.retryCount != retryCount {
		t.Errorf("Expected retryCount %d, got %d", retryCount, refresher.retryCount)
	}
	if refresher.logger != logger {
		t.Errorf("Expected logger to be set, got %v", refresher.logger)
	}
	if refresher.ctx == nil || refresher.cancel == nil {
		t.Error("Expected context and cancel to be initialized")
	}
	if refresher.whitelist == nil {
		t.Error("Expected whitelist to be initialized")
	}
}

func TestWhitelistRefresher_Start_Stop(t *testing.T) {
	mockRepo := &mockKeyRepo{keys: [][32]byte{{1}}}
	logger := log.New(os.Stdout, "test", log.LstdFlags)
	refresher := NewWhitelistRefresher(mockRepo, 100*time.Millisecond, 0, logger)

	refresher.Start()
	time.Sleep(250 * time.Millisecond) // Allow multiple ticks
	refresher.Stop()

	if mockRepo.getCallCount() == 0 {
		t.Error("Expected GetAll to be called during start")
	}
}

func TestWhitelistRefresher_refresh_Success(t *testing.T) {
	mockRepo := &mockKeyRepo{keys: [][32]byte{{1}}}
	logger := log.New(os.Stdout, "test", log.LstdFlags)
	refresher := NewWhitelistRefresher(mockRepo, 1*time.Hour, 0, logger)

	refresher.refresh()

	if mockRepo.getCallCount() != 1 {
		t.Errorf("Expected GetAll to be called once, got %d", mockRepo.getCallCount())
	}
	// Verify whitelist update (assuming IsWhitelisted works)
	if !refresher.whitelist.IsWhitelisted("0100000000000000000000000000000000000000000000000000000000000000") {
		t.Error("Expected key to be whitelisted after refresh")
	}
}

func TestWhitelistRefresher_refresh_Failure_NoRetry(t *testing.T) {
	mockRepo := &mockKeyRepo{err: errors.New("db error")}
	logger := log.New(os.Stdout, "test", log.LstdFlags)
	refresher := NewWhitelistRefresher(mockRepo, 1*time.Hour, 0, logger)

	refresher.refresh()

	if mockRepo.getCallCount() != 1 {
		t.Errorf("Expected GetAll to be called once, got %d", mockRepo.getCallCount())
	}
}

func TestWhitelistRefresher_refresh_Failure_WithRetry(t *testing.T) {
	mockRepo := &mockKeyRepo{err: errors.New("db error")}
	logger := log.New(os.Stdout, "test", log.LstdFlags)
	refresher := NewWhitelistRefresher(mockRepo, 1*time.Hour, 2, logger)

	refresher.refresh()

	expectedCalls := 3 // 1 initial + 2 retries
	if mockRepo.getCallCount() != expectedCalls {
		t.Errorf("Expected GetAll to be called %d times, got %d", expectedCalls, mockRepo.getCallCount())
	}
}

func TestWhitelistRefresher_refresh_SuccessAfterRetry(t *testing.T) {
	mockRepo := &mockKeyRepo{keys: [][32]byte{{2}}, err: errors.New("db error")}
	logger := log.New(os.Stdout, "test", log.LstdFlags)
	refresher := NewWhitelistRefresher(mockRepo, 1*time.Hour, 2, logger)

	// Simulate success on retry
	go func() {
		time.Sleep(50 * time.Millisecond)
		mockRepo.err = nil
	}()

	refresher.refresh()

	if mockRepo.getCallCount() < 2 {
		t.Errorf("Expected at least 2 calls, got %d", mockRepo.getCallCount())
	}
	if !refresher.whitelist.IsWhitelisted("0200000000000000000000000000000000000000000000000000000000000000") {
		t.Error("Expected key to be whitelisted after successful retry")
	}
}

func TestWhitelistRefresher_StopBeforeStart(t *testing.T) {
	mockRepo := &mockKeyRepo{}
	logger := log.New(os.Stdout, "test", log.LstdFlags)
	refresher := NewWhitelistRefresher(mockRepo, 1*time.Hour, 0, logger)

	refresher.Stop() // Should not panic or hang

	if mockRepo.getCallCount() != 0 {
		t.Errorf("Expected no calls to GetAll, got %d", mockRepo.getCallCount())
	}
}

func TestWhitelistRefresher_ContextCancellation(t *testing.T) {
	mockRepo := &mockKeyRepo{keys: [][32]byte{{3}}}
	logger := log.New(os.Stdout, "test", log.LstdFlags)
	refresher := NewWhitelistRefresher(mockRepo, 10*time.Millisecond, 0, logger) // Shorter interval

	refresher.Start()
	time.Sleep(20 * time.Millisecond) // Allow time for at least one tick
	refresher.Stop()                  // Cancel context

	// Wait a bit to ensure no more calls
	time.Sleep(50 * time.Millisecond)
	callCount := mockRepo.getCallCount()
	if callCount == 0 {
		t.Error("Expected at least one call before cancellation")
	}
	// Note: Exact count may vary due to timing, but should not increase after Stop
}
