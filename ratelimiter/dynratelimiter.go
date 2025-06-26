package ratelimiter

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type SimpleLogger struct {
	SimpleLogger *logrus.Logger
}

func (sl *SimpleLogger) Infoxf(_ interface{}, format string, a ...interface{}) {
	sl.SimpleLogger.Infof(format, a...)
}

// DynamicRateLimiter defines an adaptive token bucket rate limiter.
type DynamicRateLimiter interface {
	Wait(accountType string, timeout time.Duration) bool
	LogSuccess()
	LogFailure()
	GetRate() float64
	// Reduce is kept for compatibility with the original interface if needed,
	// but LogFailure is preferred.
	Reduce()
}

// dynamicRateLimiter defines an adaptive token bucket rate limiter.
type dynamicRateLimiter struct {
	ctx         context.Context
	log         *SimpleLogger
	name        string
	capacity    int     // maximum tokens
	tokens      int     // current tokens available
	refillRate  float64 // tokens per second; dynamic value
	lastRefill  time.Time
	minRate     float64 // lower bound for refill rate
	maxRate     float64 // upper bound for refill rate
	adjustStep  float64 // incremental step for increasing rate
	backoffStep float64 // decrement step for reducing rate
	mu          sync.Mutex

	// New fields for smarter adjustments
	successCount     int // Consecutive successes
	failureCount     int // Consecutive failures
	successThreshold int // Increase rate after this many successes
	failureThreshold int // Reduce rate after this many failures
}

// NewDynamicRateLimiter creates an instance with initial parameters.
func NewDynamicRateLimiter(ctx context.Context, log *SimpleLogger, name string, capacity int, initialRate, minRate, maxRate, adjustStep, backoffStep float64) *dynamicRateLimiter {
	return &dynamicRateLimiter{
		ctx:              ctx,
		log:              log,
		name:             name,
		capacity:         capacity,
		tokens:           capacity, // start with a full bucket
		refillRate:       initialRate,
		minRate:          minRate,
		maxRate:          maxRate,
		adjustStep:       adjustStep,
		backoffStep:      backoffStep,
		lastRefill:       time.Now(),
		successThreshold: 3,  // Increase rate after successes
		failureThreshold: 10, // Tolerate failures before reducing rate
	}
}

// refill adds tokens based on elapsed time and current refill rate.
func (drl *dynamicRateLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(drl.lastRefill).Seconds()
	addedTokens := int(elapsed * drl.refillRate)
	if addedTokens > 0 {
		drl.tokens += addedTokens
		if drl.tokens > drl.capacity {
			drl.tokens = drl.capacity
		}
		drl.lastRefill = now
	}
}

// Wait blocks until a token is available.
func (drl *dynamicRateLimiter) Wait(accountType string, timeout time.Duration) bool {
	start := time.Now()
	for {
		select {
		case <-drl.ctx.Done():
			return false
		default:
		}

		drl.mu.Lock()
		drl.refill()
		if drl.tokens > 0 {
			drl.tokens--
			drl.mu.Unlock()
			return true
		}
		drl.mu.Unlock()

		if accountType == "basic" && time.Since(start) >= timeout {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Reduce provides a direct way to signal overload, delegating to LogFailure.
func (drl *dynamicRateLimiter) Reduce() {
	drl.LogFailure()
}

// increaseRate contains the logic to increase the refill rate.
func (drl *dynamicRateLimiter) increaseRate() {
	newRate := drl.refillRate + drl.adjustStep
	if newRate > drl.maxRate {
		newRate = drl.maxRate
	}
	if newRate != drl.refillRate {
		drl.refillRate = newRate
		drl.log.Infoxf(nil, "Rate increased to %.2f", newRate)
	}
}

// reduceRate contains the logic to lower the refill rate.
func (drl *dynamicRateLimiter) reduceRate() {
	newRate := drl.refillRate - drl.backoffStep
	if newRate < drl.minRate {
		newRate = drl.minRate
	}
	if newRate != drl.refillRate {
		drl.refillRate = newRate
		drl.log.Infoxf(nil, "Rate reduced to %.2f", newRate)
	}
}

// LogSuccess is called upon successful calls.
func (drl *dynamicRateLimiter) LogSuccess() {
	drl.mu.Lock()
	defer drl.mu.Unlock()

	drl.failureCount = 0 // Reset failure count on success
	drl.successCount++

	if drl.successCount >= drl.successThreshold {
		drl.increaseRate()
		drl.successCount = 0 // Reset after increasing
	}
}

// LogFailure is called on overload (e.g., after 429 responses).
func (drl *dynamicRateLimiter) LogFailure() {
	drl.mu.Lock()
	defer drl.mu.Unlock()

	drl.successCount = 0 // Reset success count on failure
	drl.failureCount++

	if drl.failureCount >= drl.failureThreshold {
		drl.reduceRate()
		// We can keep the failure count high to prevent immediate rate increases
		// or reset it to start the count again. Let's reset it.
		// drl.failureCount = 0
	}
}

// GetRate returns the current refill rate.
func (drl *dynamicRateLimiter) GetRate() float64 {
	drl.mu.Lock()
	defer drl.mu.Unlock()
	return drl.refillRate
}
