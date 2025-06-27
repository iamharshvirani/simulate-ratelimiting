package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"os"
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
			125.0, // initial rate
			60.0,  // min rate
			125.0, // max rate
			25,    // adjust step
			15,    // backoff step

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

	// ---------------------------------------------------------------------------
	// Set up per-run logging to both console and a timestamped log file.
	// ---------------------------------------------------------------------------
	logTimestamp := time.Now().Format("20060102_150405")
	logFileName := fmt.Sprintf("simulation_%s.log", logTimestamp)
	logFile, err := os.Create("./logs/" + logFileName)
	if err != nil {
		log.Fatalf("unable to create log file %s: %v", logFileName, err)
	}
	defer logFile.Close()

	multiOut := io.MultiWriter(os.Stdout, logFile)
	// Standard library log → file + console
	log.SetOutput(multiOut)

	simDuration := 120 // seconds
	numPods := 6       // number of VA Pipeline pods
	gpuMaxCapacityPerPod := 125
	numWorkers := 90 // worker pool size
	processingDelay := 100 * time.Millisecond

	// Logrus setup → file + console
	l := logrus.New()
	l.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	l.SetOutput(multiOut)
	logger := &ratelimiter.SimpleLogger{SimpleLogger: l}

	cluster := NewPipelineCluster(numPods, gpuMaxCapacityPerPod, ctx, logger)

	channelBufferSize := 5000

	requestChan := make(chan struct {
		cameraId  string
		timestamp time.Time
		// }, numWorkers)
	}, channelBufferSize)
	metricsChan := make(chan secondMetrics, simDuration)

	// Start worker pool
	var workerWg sync.WaitGroup
	workerWg.Add(numWorkers)

	// newGenerateLoadConfig := generateLoadConfig{
	// 	pattern:        "sine",
	// 	baseline:       200.0,
	// 	amplitude:      60.0,
	// 	period:         90.0,
	// 	jitterPercent:  20,
	// 	peakSecond:     55,
	// 	peakMultiplier: 2.5,
	// }

	newGenerateSineLoadConfig := generateLoadConfig{
		pattern:        "sine",
		baseline:       450.0,
		amplitude:      150.0,
		period:         60.0,
		jitterPercent:  7,
		peakSecond:     90,
		peakMultiplier: 1.1,
	}

	// newGenerateStepLoadConfig := generateLoadConfig{
	// 	pattern:       "step",
	// 	baseline:      240.0,
	// 	amplitude:     5.0,
	// 	stepDuration:  10,
	// 	jitterPercent: 20,
	// }

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
					if !pod.rateLimiter.Wait("", 20*time.Millisecond) {
						pod.rateLimiter.LogFailure()
						continue
					}

					// Simulate processing
					// time.Sleep(processingDelay + time.Duration(rand.Intn(5))*time.Millisecond)

					// Try to process on the pod
					if pod.Process() {
						pod.rateLimiter.LogSuccess()
						time.Sleep(processingDelay + time.Duration(rand.Float64()*30)*time.Millisecond)
					} else {
						// pod.rateLimiter.LogFailure()
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
				eventsThisSecond := generateLoad(t, newGenerateSineLoadConfig)

				// Generate camera IDs and dispatch events
				if eventsThisSecond > 0 {
					interval := time.Second / time.Duration(eventsThisSecond)
					producerWg.Add(1)
					go func(numEvents int, ivl time.Duration) {
						defer producerWg.Done()
						for i := 0; i < numEvents; i++ {
							cameraId := fmt.Sprintf("cam_%d", rand.Intn(500))
							requestChan <- struct {
								cameraId  string
								timestamp time.Time
							}{cameraId, time.Now()}
							// time.Sleep(ivl)
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
					log.Printf("Pod %s: Processed=%d", pod.id, processed)
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

	err = plotmetrics.PlotResults(seconds, totalEventsArr, inferencedEventsArr, maxCapacityArr, rateArr)
	if err != nil {
		log.Fatalf("Error plotting results: %v", err)
	}
	log.Println("Graph generated.")
}

func generateLoad(second int, loadConfig generateLoadConfig) int {
	// baseLoad := loadConfig.baseline + loadConfig.amplitude*math.Sin(2*math.Pi*float64(second)/loadConfig.period)
	// jitter := (rand.Float64() - 0.5) * (float64(loadConfig.jitterPercent) / 100.0) * baseLoad
	// loadWithJitter := baseLoad + jitter
	// if second == loadConfig.peakSecond {
	// 	loadWithJitter *= loadConfig.peakMultiplier
	// }
	// return int(math.Max(loadWithJitter, 0))

	var baseLoad float64

	switch loadConfig.pattern {
	case "sine":
		// Sine wave pattern
		baseLoad = loadConfig.baseline + loadConfig.amplitude*math.Sin(2*math.Pi*float64(second)/loadConfig.period)
	case "step":
		// Step pattern - alternates between high and low load
		if (second/loadConfig.stepDuration)%2 == 0 {
			baseLoad = loadConfig.baseline + loadConfig.amplitude
		} else {
			baseLoad = loadConfig.baseline - loadConfig.amplitude
		}
	case "constant":
		// Constant load with optional peak
		baseLoad = loadConfig.baseline
	default:
		// Default to sine wave if pattern is not recognized
		baseLoad = loadConfig.baseline + loadConfig.amplitude*math.Sin(2*math.Pi*float64(second)/loadConfig.period)
	}

	// Apply jitter if enabled
	jitter := 0.0
	if loadConfig.jitterPercent > 0 {
		jitter = (rand.Float64() - 0.5) * (float64(loadConfig.jitterPercent) / 100.0) * baseLoad
	}

	// Apply peak multiplier if this is the peak second
	if second == loadConfig.peakSecond {
		baseLoad *= loadConfig.peakMultiplier
	}

	// Calculate final load with jitter and ensure it's not negative
	loadWithJitter := baseLoad + jitter
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
	pattern        string
	baseline       float64
	jitterPercent  int
	peakSecond     int
	peakMultiplier float64

	// Sine wave parameters
	amplitude float64 // amplitude of sine wave
	period    float64 // period of sine wave in seconds

	// Step pattern parameters
	stepDuration int
}
