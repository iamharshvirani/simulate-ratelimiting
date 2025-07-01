package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/iamharshvirani/simulate-ratelimiting/internal/pipeline"
	"github.com/iamharshvirani/simulate-ratelimiting/pkg/ratelimiter"
	"github.com/iamharshvirani/simulate-ratelimiting/pkg/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type requestMessage struct {
	cameraId  string
	timestamp time.Time
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

	cluster := pipeline.NewPipelineCluster(numPods, gpuMaxCapacityPerPod, ctx, logger)

	channelBufferSize := 5000

	requestChan := make(chan requestMessage, channelBufferSize)
	// channelBufferSize -> numWorkers
	metricsChan := make(chan utils.SecondMetrics, simDuration)

	var workerWg sync.WaitGroup
	workerWg.Add(numWorkers)

	// Use predefined load configuration instead of defining it inline
	loadConfigName := "sine_low"
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
					if !pod.GetRateLimiter().Wait("", 20*time.Millisecond) {
						pod.GetRateLimiter().LogFailure()
						continue
					}

					// Simulate processing
					// time.Sleep(processingDelay + time.Duration(rand.Intn(5))*time.Millisecond)

					// Try to process on the pod
					if pod.Process() {
						pod.GetRateLimiter().LogSuccess()
						time.Sleep(processingDelay + time.Duration(rand.Float64()*30)*time.Millisecond)
					} else {
						pod.GetRateLimiter().LogFailure()
					}
				}
			}
		}()
	}

	// Start the simulation in a separate goroutine
	go runSimulation(ctx, simDuration, loadConfig, requestChan, metricsChan, cluster, numPods)

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

func runSimulation(ctx context.Context, simDuration int, loadConfig utils.GenerateLoadConfig, requestChan chan<- requestMessage,
	metricsChan chan<- utils.SecondMetrics, cluster *pipeline.PipelineCluster, numPods int) {
	log.Println("Starting simulation...")
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
					requestChan <- requestMessage{
						cameraId:  cameraId,
						timestamp: time.Now(),
					}
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
		ratesPerPod := make([]float64, numPods)
		for i, pod := range cluster.GetPods() {
			curr := pod.GetTotalProcessed()
			delta := int(curr - prevTotals[i])
			prevTotals[i] = curr

			// Get and log rate limiter metrics for this pod
			metrics := pod.GetRateLimiter().GetMetrics()
			log.Printf("Pod %s: Processed=%d Tokens=%.2f RefillRate=%.2f", pod.GetId(), delta, metrics.Tokens, metrics.RefillRate)

			totalProcessed += delta
			rate := pod.GetRateLimiter().GetRate()
			totalRate += rate
			ratesPerPod[i] = rate
		}

		log.Printf("Second %d: Target Load=%-4d | totalProcessed=%d | Rate: %.2f",
			t, eventsThisSecond, totalProcessed, totalRate)

		metricsChan <- utils.SecondMetrics{
			TotalEvents:      eventsThisSecond,
			InferencedEvents: totalProcessed,
			RatesPerPod:      ratesPerPod,
		}
	}
	producerWg.Wait()
	close(requestChan)
	close(metricsChan)
}
