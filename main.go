package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/iamharshvirani/simulate-ratelimiting/plotmetrics"
	"github.com/iamharshvirani/simulate-ratelimiting/ratelimiter"
	"github.com/sirupsen/logrus"
)

// VAPipelinePod simulates a VA Pipeline pod with its own capacity
type VAPipelinePod struct {
	mu               sync.Mutex
	id               string
	capacityPerSec   int
	processedThisSec int
	lastTick         time.Time
	rateLimiter      ratelimiter.DynamicRateLimiter
}

func NewVAPipelinePod(id string, capacity int, ctx context.Context, logger *ratelimiter.SimpleLogger) *VAPipelinePod {
	return &VAPipelinePod{
		id:             id,
		capacityPerSec: capacity,
		lastTick:       time.Now(),
		rateLimiter: ratelimiter.NewDynamicRateLimiter(
			ctx,
			logger,
			"pod_"+id,
			125,   // capacity
			100.0, // initial rate
			80.0,  // min rate
			125.0, // max rate
			15,    // adjust step
			30,    // backoff step

			// 125,   // capacity (bucket size)
			// 100.0, // initial refill rate tokens/sec
			// 30.0,  // min rate
			// 150.0, // max rate
			// 16,    // adjust step
			// 20,    // backoff step
		),
	}
}

func (p *VAPipelinePod) Process() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if time.Since(p.lastTick) >= time.Second {
		p.lastTick = time.Now()
		p.processedThisSec = 0
	}

	if p.processedThisSec < p.capacityPerSec {
		p.processedThisSec++
		return true
	}
	return false
}

// PipelineCluster manages multiple VA Pipeline pods
type PipelineCluster struct {
	pods []*VAPipelinePod
}

func NewPipelineCluster(numPods int, capacityPerPod int, ctx context.Context, logger *ratelimiter.SimpleLogger) *PipelineCluster {
	cluster := &PipelineCluster{
		pods: make([]*VAPipelinePod, numPods),
	}
	for i := 0; i < numPods; i++ {
		cluster.pods[i] = NewVAPipelinePod(fmt.Sprintf("pod-%d", i), capacityPerPod, ctx, logger)
	}
	return cluster
}

func (c *PipelineCluster) GetPod(cameraId string) *VAPipelinePod {
	podIndex := int(cameraHash(cameraId)) % len(c.pods)
	return c.pods[podIndex]
}

// Hash function for camera ID distribution
func cameraHash(cameraId string) uint32 {
	h := uint32(0)
	for i := 0; i < len(cameraId); i++ {
		h = h*31 + uint32(cameraId[i])
	}
	return h
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	simDuration := 120 // seconds
	numPods := 2       // number of VA Pipeline pods
	gpuMaxCapacityPerPod := 125
	numWorkers := 75 // worker pool size
	processingDelay := 20 * time.Millisecond

	l := logrus.New()
	l.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	logger := &ratelimiter.SimpleLogger{SimpleLogger: l}

	cluster := NewPipelineCluster(numPods, gpuMaxCapacityPerPod, ctx, logger)

	requestChan := make(chan struct {
		cameraId  string
		timestamp time.Time
	}, numWorkers)
	metricsChan := make(chan secondMetrics, simDuration)

	// Start worker pool
	var workerWg sync.WaitGroup
	workerWg.Add(numWorkers)

	newGenerateLoadConfig := generateLoadConfig{
		baseline:       200.0,
		amplitude:      60.0,
		period:         90.0,
		jitterPercent:  20,
		peakSecond:     55,
		peakMultiplier: 2.5,
	}
	for i := 0; i < numWorkers; i++ {
		go func() {
			defer workerWg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case req, ok := <-requestChan:
					if !ok {
						return
					}

					// Get the pod for this camera (simulating sticky routing)
					pod := cluster.GetPod(req.cameraId)

					// Try to get rate limiter token
					if !pod.rateLimiter.Wait("basic", 50*time.Millisecond) {
						continue
					}

					// Simulate processing
					time.Sleep(processingDelay + time.Duration(rand.Intn(5))*time.Millisecond)

					// Try to process on the pod
					if pod.Process() {
						pod.rateLimiter.LogSuccess()
					} else {
						pod.rateLimiter.LogFailure()
					}
				}
			}
		}()
	}

	// Start event generation and metrics collection
	log.Println("Starting simulation...")
	go func() {
		var producerWg sync.WaitGroup
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for t := 0; t < simDuration; t++ {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				eventsThisSecond := generateLoad(t, newGenerateLoadConfig)

				// Generate camera IDs and dispatch events
				if eventsThisSecond > 0 {
					interval := time.Second / time.Duration(eventsThisSecond)
					producerWg.Add(1)
					go func(numEvents int, ivl time.Duration) {
						defer producerWg.Done()
						for i := 0; i < numEvents; i++ {
							cameraId := fmt.Sprintf("cam_%d", rand.Intn(1000))
							requestChan <- struct {
								cameraId  string
								timestamp time.Time
							}{cameraId, time.Now()}
							time.Sleep(ivl)
						}
					}(eventsThisSecond, interval)
				}

				// Collect metrics from all pods
				var totalProcessed int
				var totalRate float64
				for _, pod := range cluster.pods {
					pod.mu.Lock()
					processed := pod.processedThisSec
					rate := pod.rateLimiter.GetRate()
					pod.mu.Unlock()

					totalProcessed += processed
					totalRate += rate
				}

				// log.Printf("Second %d: Target=%d, Processed=%d, AvgRate=%.2f",
				// 	t, eventsThisSecond, totalProcessed, totalRate/float64(numPods))

				log.Printf("Second %d: Target Load=%-4d | totalProcessed=%d | Rate: %.2f",
					t, eventsThisSecond, totalProcessed, totalRate/float64(numPods))

				metricsChan <- secondMetrics{
					totalEvents:      eventsThisSecond,
					inferencedEvents: totalProcessed,
					finalRate:        totalRate / float64(numPods),
				}
			}
		}
		producerWg.Wait()
		close(requestChan)
		close(metricsChan)
	}()

	// Collect and plot results
	var allMetrics []secondMetrics
	for m := range metricsChan {
		allMetrics = append(allMetrics, m)
	}

	workerWg.Wait()
	log.Println("Simulation complete.")

	// Prepare plotting data
	var seconds, totalEventsArr, inferencedEventsArr, maxCapacityArr, rateArr []float64
	for i, m := range allMetrics {
		seconds = append(seconds, float64(i))
		totalEventsArr = append(totalEventsArr, float64(m.totalEvents))
		inferencedEventsArr = append(inferencedEventsArr, float64(m.inferencedEvents))
		maxCapacityArr = append(maxCapacityArr, float64(gpuMaxCapacityPerPod*numPods))
		rateArr = append(rateArr, m.finalRate)
	}

	err := plotmetrics.PlotResults(seconds, totalEventsArr, inferencedEventsArr, maxCapacityArr, rateArr)
	if err != nil {
		log.Fatalf("Error plotting results: %v", err)
	}
	log.Println("Graph generated.")
}

func generateLoad(second int, loadConfig generateLoadConfig) int {
	baseLoad := loadConfig.baseline + loadConfig.amplitude*math.Sin(2*math.Pi*float64(second)/loadConfig.period)
	jitter := (rand.Float64() - 0.5) * (float64(loadConfig.jitterPercent) / 100.0) * baseLoad
	loadWithJitter := baseLoad + jitter
	if second == loadConfig.peakSecond {
		loadWithJitter *= loadConfig.peakMultiplier
	}
	return int(math.Max(loadWithJitter, 0))
}

type secondMetrics struct {
	totalEvents          int
	inferencedEvents     int
	droppedByRateLimiter int
	droppedByPipeline    int
	finalRate            float64
}

type generateLoadConfig struct {
	baseline, amplitude, period float64
	jitterPercent               int
	peakSecond                  int
	peakMultiplier              float64
}
