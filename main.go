package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iamharshvirani/simulate-ratelimiting/ratelimiter"
	"github.com/iamharshvirani/simulate-ratelimiting/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type VAPipelinePod struct {
	mu               sync.Mutex
	id               string
	capacityPerSec   int
	processedThisSec int
	lastTickUnix     int64
	rateLimiter      ratelimiter.DynamicRateLimiter
	totalProcessed   uint64 // monotonic counter for accurate metrics
}

func NewVAPipelinePod(id string, capacity int, ctx context.Context, logger *ratelimiter.SimpleLogger) *VAPipelinePod {
	return &VAPipelinePod{
		id:             id,
		capacityPerSec: capacity,
		lastTickUnix:   time.Now().Unix(),
		rateLimiter: ratelimiter.NewDynamicRateLimiter(
			ctx,
			logger,
			"pod_"+id,
			125,   // capacity
			125.0, // initial rate
			60.0,  // min rate
			125.0, // max rate
			5,     // adjust step
			7,     // backoff step
		),
	}
}

func (p *VAPipelinePod) Process() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	currentSecond := now.Unix()

	if p.lastTickUnix != currentSecond {
		p.lastTickUnix = currentSecond
		p.processedThisSec = 0
	}

	if p.processedThisSec < p.capacityPerSec {
		p.processedThisSec++
		atomic.AddUint64(&p.totalProcessed, 1)
		return true
	}
	return false
}

// GetTotalProcessed returns the monotonic processed counter.
func (p *VAPipelinePod) GetTotalProcessed() uint64 {
	return atomic.LoadUint64(&p.totalProcessed)
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
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./")

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file: %s", err)
	}

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

	simDuration := viper.GetInt("simulation.duration")
	numPods := viper.GetInt("simulation.numPods")
	gpuMaxCapacityPerPod := viper.GetInt("simulation.gpuMaxCapacityPerPod")
	numWorkers := viper.GetInt("simulation.numWorkers")
	processingDelay := viper.GetDuration("simulation.processingDelay")

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
	metricsChan := make(chan utils.SecondMetrics, simDuration)

	var workerWg sync.WaitGroup
	workerWg.Add(numWorkers)

	// Use predefined load configuration instead of defining it inline
	loadConfigName := "constant_high_above_capacity"
	loadConfig, err := utils.GetLoadConfig(loadConfigName)
	if err != nil {
		log.Fatalf("Error getting load configuration: %v", err)
	}
	log.Printf("Using load configuration: %s", loadConfigName)

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
		prevTotals := make([]uint64, numPods)

		// Align to the next exact wall-clock second before starting the loop
		firstBoundary := time.Now().Truncate(time.Second).Add(time.Second)
		time.Sleep(time.Until(firstBoundary))

		for t := 0; t < simDuration; t++ {
			select {
			case <-ctx.Done():
				return
			default:
			}

			secondStart := time.Now() // precisely at wall-clock boundary

			// Generate load for this second (as before)
			eventsThisSecond := utils.GenerateLoad(t, loadConfig)

			// Generate camera IDs and dispatch events
			if eventsThisSecond > 0 {
				interval := time.Second / time.Duration(eventsThisSecond+1) // +1 to avoid div-by-zero

				producerWg.Add(1)
				go func(numEvents int, ivl time.Duration) {
					defer producerWg.Done()
					for i := 0; i < numEvents; i++ {
						cameraId := fmt.Sprintf("cam_%d", rand.Intn(250))
						requestChan <- struct {
							cameraId  string
							timestamp time.Time
						}{cameraId: cameraId, timestamp: time.Now()}
						time.Sleep(ivl)
					}
				}(eventsThisSecond, interval)
			}

			// Sleep until the end of this wall-clock second
			nextBoundary := secondStart.Truncate(time.Second).Add(time.Second)
			time.Sleep(time.Until(nextBoundary))

			// Collect metrics using monotonic counters
			var totalProcessed int
			var totalRate float64
			for i, pod := range cluster.pods {
				curr := pod.GetTotalProcessed()
				delta := int(curr - prevTotals[i])
				prevTotals[i] = curr
				log.Printf("Pod %s: Processed=%d", pod.id, delta)
				totalProcessed += delta
				totalRate += pod.rateLimiter.GetRate()
			}

			log.Printf("Second %d: Target Load=%-4d | totalProcessed=%d | Rate: %.2f",
				t, eventsThisSecond, totalProcessed, totalRate)

			metricsChan <- utils.SecondMetrics{
				TotalEvents:      eventsThisSecond,
				InferencedEvents: totalProcessed,
				FinalRate:        totalRate / float64(numPods),
			}
		}
		producerWg.Wait()
		close(requestChan)
		close(metricsChan)
	}()

	// Collect and plot results
	var allMetrics []utils.SecondMetrics
	for m := range metricsChan {
		allMetrics = append(allMetrics, m)
	}

	workerWg.Wait()
	log.Println("Simulation complete.")

	err = utils.GeneratePlotFromMetrics(allMetrics, gpuMaxCapacityPerPod, numPods)
	if err != nil {
		log.Fatalf("Error plotting results: %v", err)
	}
	log.Println("Graph generated.")
}
