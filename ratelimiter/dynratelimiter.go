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
	// Wait blocks until a token is available.
	// For premium accounts, it waits indefinitely.
	// For basic accounts, it waits up to the provided timeout.
	Wait(accountType string, timeout time.Duration) bool
	Reduce()
	IncreaseRate()
	ReduceRate()
	GetRate() float64
}

// DynamicRateLimiter defines an adaptive token bucket rate limiter.
type dynamicRateLimiter struct {
	ctx         context.Context
	log         *logrus.Logger
	name        string
	capacity    int     // maximum tokens (usually 1 for our case)
	tokens      int     // current tokens available
	refillRate  float64 // tokens per second; dynamic value
	lastRefill  time.Time
	minRate     float64 // lower bound for refill rate
	maxRate     float64 // upper bound for refill rate
	adjustStep  float64 // incremental step for increasing rate
	backoffStep float64 // decrement step for reducing rate
	mu          sync.Mutex
}

// NewDynamicRateLimiter creates an instance with initial parameters.
func NewDynamicRateLimiter(ctx context.Context, log *SimpleLogger, name string, capacity int, initialRate, minRate, maxRate, adjustStep, backoffStep float64) *dynamicRateLimiter {
	return &dynamicRateLimiter{
		ctx:         ctx,
		name:        name,
		capacity:    capacity,
		tokens:      capacity, // start with a full bucket
		refillRate:  initialRate,
		minRate:     minRate,
		maxRate:     maxRate,
		adjustStep:  adjustStep,
		backoffStep: backoffStep,
		lastRefill:  time.Now(),
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
// For "premium" accounts, it waits indefinitely.
// For "basic" accounts, it waits up to the specified timeout and returns false if it times out.
func (drl *dynamicRateLimiter) Wait(accountType string, timeout time.Duration) bool {
	start := time.Now()
	for {
		// Check for cancellation
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

		// For basic accounts, check if we've exceeded our wait timeout.
		if accountType == "basic" && time.Since(start) >= timeout {
			return false
		}
		// Premium accounts or still within timeout: sleep briefly and try again.
		time.Sleep(10 * time.Millisecond)
	}
}

// Reduce pushes a skip signal. In this dynamic limiter, we delegate overload handling
// to ReduceRate(), so we can simply call that method.
func (drl *dynamicRateLimiter) Reduce() {
	drl.ReduceRate()
}

// IncreaseRate should be called upon successful calls (2xx and low latency)
// to gradually increase the refill rate up to maxRate.
func (drl *dynamicRateLimiter) IncreaseRate() {
	drl.mu.Lock()
	defer drl.mu.Unlock()
	newRate := drl.refillRate + drl.adjustStep
	if newRate > drl.maxRate {
		newRate = drl.maxRate
	}
	if newRate != drl.refillRate {
		drl.refillRate = newRate
	}
}

// ReduceRate is called on overload (e.g., after 429 responses)
// to lower the refill rate down to minRate.
func (drl *dynamicRateLimiter) ReduceRate() {
	drl.mu.Lock()
	defer drl.mu.Unlock()
	newRate := drl.refillRate - drl.backoffStep
	if newRate < drl.minRate {
		newRate = drl.minRate
	}
	if newRate != drl.refillRate {
		drl.refillRate = newRate
	}
}

// GetRate returns the current refill rate.
func (drl *dynamicRateLimiter) GetRate() float64 {
	drl.mu.Lock()
	defer drl.mu.Unlock()
	return drl.refillRate
}
