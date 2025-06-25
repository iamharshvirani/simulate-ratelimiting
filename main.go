package main

import (
	"context"
	"log"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/iamharshvirani/simulate-ratelimiting/plotmetrics"
	"github.com/iamharshvirani/simulate-ratelimiting/ratelimiter"
	"github.com/sirupsen/logrus"
)

// VAPipelineSimulator remains the same.
type VAPipelineSimulator struct {
	mu               sync.Mutex
	capacityPerSec   int
	processedThisSec int
	lastTick         time.Time
}

func NewVAPipelineSimulator(capacity int) *VAPipelineSimulator {
	return &VAPipelineSimulator{
		capacityPerSec: capacity,
		lastTick:       time.Now(),
	}
}

func (s *VAPipelineSimulator) Process() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if time.Since(s.lastTick) >= time.Second {
		s.lastTick = time.Now()
		s.processedThisSec = 0
	}

	if s.processedThisSec < s.capacityPerSec {
		s.processedThisSec++
		return true
	}
	return false
}

func generateLoad(second int, baseline, amplitude, period float64, jitterPercent int, peakSecond int, peakMultiplier float64) int {
	baseLoad := baseline + amplitude*math.Sin(2*math.Pi*float64(second)/period)
	jitter := (rand.Float64() - 0.5) * (float64(jitterPercent) / 100.0) * baseLoad
	loadWithJitter := baseLoad + jitter
	if second == peakSecond {
		loadWithJitter *= peakMultiplier
	}
	return int(math.Max(loadWithJitter, 0))
}

// Struct to hold results for each second.
type secondMetrics struct {
	totalEvents          int
	inferencedEvents     int
	droppedByRateLimiter int
	droppedByPipeline    int
	finalRate            float64
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// --- Simulation Parameters ---
	simDuration := 120
	gpuMaxCapacity := 125
	processingDelay := 10 * time.Millisecond
	numWorkers := 200 // A pool of workers to process requests.

	l := logrus.New()
	l.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	logger := &ratelimiter.SimpleLogger{SimpleLogger: l}

	rateLimiter := ratelimiter.NewDynamicRateLimiter(
		ctx,
		logger,
		"simLimiter",
		125,   // capacity (bucket size)
		100.0, // initial refill rate tokens/sec
		30.0,  // min rate
		150.0, // max rate
		16,    // adjust step
		20,    // backoff step
	)

	pipeline := NewVAPipelineSimulator(gpuMaxCapacity)
	requestChan := make(chan struct{}, numWorkers) // Channel for incoming requests
	metricsChan := make(chan secondMetrics, simDuration)

	var workerWg sync.WaitGroup

	// --- Start Worker Pool ---
	workerWg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func() {
			defer workerWg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-requestChan:
					if !ok {
						return
					}

					// This logic is now executed by each worker.
					if !rateLimiter.Wait("basic", 20*time.Millisecond) {
						// Dropped by our own rate limiter
						continue
					}
					time.Sleep(processingDelay)
					if pipeline.Process() {
						rateLimiter.LogSuccess()
					} else {
						rateLimiter.LogFailure() // "429" signal
					}
				}
			}
		}()
	}

	// --- Start Producer and Metrics Collector ---
	log.Println("Starting simulation...")
	go func() {
		// This goroutine produces events and collects metrics each second.
		var producerWg sync.WaitGroup // WaitGroup for drip-feed goroutines
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for t := 0; t < simDuration; t++ {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// --- Producer Logic ---
				eventsThisSecond := generateLoad(t, 80.0, 60.0, 90.0, 30, 45, 3.0)

				// Drip-feed events into the channel over one second.
				if eventsThisSecond > 0 {
					interval := time.Second / time.Duration(eventsThisSecond)
					producerWg.Add(1)
					go func(numEvents int, ivl time.Duration) {
						defer producerWg.Done()
						eventTicker := time.NewTicker(ivl)
						defer eventTicker.Stop()
						for j := 0; j < numEvents; j++ {
							select {
							case requestChan <- struct{}{}:
							case <-ctx.Done():
								return
							}
							// Also check for context done while waiting for the ticker
							// to prevent goroutine leaks.
							select {
							case <-eventTicker.C:
							case <-ctx.Done():
								return
							}
						}
					}(eventsThisSecond, interval)
				}

				// --- Metrics Collector Logic ---
				// To get accurate metrics, we can peek into the pipeline simulator.
				// This is a simplification for the simulation.
				pipeline.mu.Lock()
				inferenced := pipeline.processedThisSec
				pipeline.mu.Unlock()

				// Calculating drops is harder now. We log the state instead.
				log.Printf("Second %d: Target Load=%-4d | Inferenced (approx)=%-4d | Rate: %.2f",
					t, eventsThisSecond, inferenced, rateLimiter.GetRate())

				metricsChan <- secondMetrics{
					totalEvents:      eventsThisSecond,
					inferencedEvents: inferenced,
					finalRate:        rateLimiter.GetRate(),
				}
			}
		}
		// Wait for all event-producing goroutines to finish before closing channels.
		producerWg.Wait()
		close(requestChan) // Signal workers to stop
		close(metricsChan)
	}()

	// --- Wait for results ---
	var allMetrics []secondMetrics
	for sm := range metricsChan {
		allMetrics = append(allMetrics, sm)
	}

	workerWg.Wait() // Wait for all workers to finish
	log.Println("Simulation complete.")

	// --- Plotting ---
	var seconds, totalEventsArr, inferencedEventsArr, maxCapacityArr, rateArr []float64
	for i, m := range allMetrics {
		seconds = append(seconds, float64(i))
		totalEventsArr = append(totalEventsArr, float64(m.totalEvents))
		inferencedEventsArr = append(inferencedEventsArr, float64(m.inferencedEvents))
		maxCapacityArr = append(maxCapacityArr, float64(gpuMaxCapacity))
		rateArr = append(rateArr, m.finalRate)
	}

	err := plotmetrics.PlotResults(seconds, totalEventsArr, inferencedEventsArr, maxCapacityArr, rateArr)
	if err != nil {
		log.Fatalf("Error plotting results: %v", err)
	}
	log.Println("Graph generated.")
}
