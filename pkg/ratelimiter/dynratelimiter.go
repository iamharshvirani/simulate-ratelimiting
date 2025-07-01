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
	Reduce()
	GetMetrics() Metrics
}

// Metrics holds the internal state of the rate limiter for monitoring.
type Metrics struct {
	Tokens     float64
	RefillRate float64
	Capacity   float64
}

// dynamicRateLimiter defines an adaptive token bucket rate limiter.
type dynamicRateLimiter struct {
	ctx         context.Context
	log         *SimpleLogger
	name        string
	capacity    float64 // maximum tokens
	tokens      float64 // current tokens available
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

	lastIncrease time.Time
	lastDecrease time.Time
}

// NewDynamicRateLimiter creates an instance with initial parameters.
func NewDynamicRateLimiter(ctx context.Context, log *SimpleLogger, name string, capacity float64, initialRate, minRate, maxRate, adjustStep, backoffStep float64) *dynamicRateLimiter {
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
		successThreshold: 5, // Increase rate after successes
		failureThreshold: 3, // Tolerate failures before reducing rate
		lastIncrease:     time.Now(),
		lastDecrease:     time.Now(),
	}
}

// refill adds tokens based on elapsed time and current refill rate.
func (drl *dynamicRateLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(drl.lastRefill).Seconds()
	addedTokens := elapsed * drl.refillRate
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
	// start := time.Now()
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
			if drl.tokens < 0 {
				drl.tokens = 0
			}
			drl.mu.Unlock()
			return true
		}
		drl.mu.Unlock()
		time.Sleep(1 * time.Millisecond)
		continue

		// if accountType == "basic" && time.Since(start) >= timeout {
		// 	return false
		// }
		// time.Sleep(10 * time.Millisecond)
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
		// drl.log.Infoxf(nil, "Rate increased to %.2f", newRate)
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
		// drl.log.Infoxf(nil, "Rate reduced to %.2f", newRate)
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

		// if time.Since(drl.lastIncrease) >= time.Second {
		// 	drl.increaseRate()
		// 	drl.successCount = 0
		// 	drl.lastIncrease = time.Now()
		// }
	}

	// drl.mu.Lock()
	// defer drl.mu.Unlock()

	// // If there is a "failure debt", a success pays it down by one.
	// // The system cannot begin counting successes until the debt is cleared.
	// if drl.failureCount > 0 {
	// 	drl.failureCount--
	// 	drl.successCount = 0 // Ensure we don't count successes while in debt.
	// 	return
	// }

	// // Only if there is no failure debt can we start counting successes.
	// drl.successCount++

	// if drl.successCount >= drl.successThreshold {
	// 	drl.increaseRate()
	// 	drl.successCount = 0 // Reset after increasing.
	// }
}

// LogFailure is called on overload (e.g., after 429 responses).
func (drl *dynamicRateLimiter) LogFailure() {
	// drl.mu.Lock()
	// defer drl.mu.Unlock()

	// drl.successCount = 0 // Reset success count on failure
	// drl.failureCount++

	// if drl.failureCount >= drl.failureThreshold {
	// 	drl.reduceRate()
	// 	// We can keep the failure count high to prevent immediate rate increases
	// 	// or reset it to start the count again. Let's reset it.
	// 	// drl.failureCount = 0
	// }

	drl.mu.Lock()
	defer drl.mu.Unlock()

	drl.successCount = 0 // A failure always resets any success streak.
	drl.failureCount++

	if drl.failureCount >= drl.failureThreshold {
		if time.Since(drl.lastDecrease) >= time.Second {
			drl.reduceRate()
			drl.failureCount = 0
			drl.lastDecrease = time.Now()
		}
		// drl.reduceRate()
		// By NOT resetting the failureCount here, we create a "cooldown".
		// The system now has a debt of `failureThreshold` successes that
		// must be processed before the rate can increase again.
	}
}

// GetRate returns the current refill rate.
func (drl *dynamicRateLimiter) GetRate() float64 {
	drl.mu.Lock()
	defer drl.mu.Unlock()
	return drl.refillRate
}

// GetMetrics returns the current metrics of the rate limiter.
func (drl *dynamicRateLimiter) GetMetrics() Metrics {
	drl.mu.Lock()
	defer drl.mu.Unlock()
	return Metrics{
		Tokens:     drl.tokens,
		RefillRate: drl.refillRate,
		Capacity:   drl.capacity,
	}
}
