package main

import (
	"context"
	"fmt"
	"image/color"
	"log"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/iamharshvirani/simulate-ratelimiting/ratelimiter"
	"github.com/sirupsen/logrus"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

// main runs a simulation of the DynamicRateLimiter to demonstrate its behavior and
// plot the results. It simulates a sine wave of events per second, and after each
// second, it adjusts the rate limiter based on the ratio of successfully inferenced
// events to total events. The results are plotted to a PNG file.
func main() {
	ctx := context.Background()
	simDuration := 30                        // simulation duration in seconds (adjust as needed)
	simInterval := time.Second               // aggregate data per second
	gpuMaxCapacity := 125                    // maximum fps (for reference in the graph)
	numPods := 3                             // number of VA pipeline pods available
	processingDelay := 10 * time.Millisecond // simulated processing delay per event

	l := logrus.New()
	l.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	logger := &ratelimiter.SimpleLogger{SimpleLogger: l}

	rateLimiter := ratelimiter.NewDynamicRateLimiter(
		ctx,
		logger,
		"simLimiter",
		125,   // capacity
		75.0,  // initial refill rate tokens/sec
		10.0,  // min rate
		120.0, // max rate
		4,     // adjust step
		9,     // backoff step
	)

	// Semaphore to simulate available VA pipeline pod slots.
	// Only numPods events can be processed concurrently.
	podSemaphore := make(chan struct{}, numPods)
	// Pre-fill semaphore with numPods tokens.
	for i := 0; i < numPods; i++ {
		podSemaphore <- struct{}{}
	}

	// Sine wave parameters for varying event rate.
	baseline := 50.0
	amplitude := 100.0
	period := 60.0 // period in seconds

	var mu sync.Mutex
	var seconds []float64
	var totalEventsArr []float64
	var inferencedEventsArr []float64
	var maxCapacityArr []float64

	log.Println("Starting simulation...")
	simStart := time.Now()
	// For each simulation second, generate events and process them concurrently.
	for t := 0; t < simDuration; t++ {
		avgEvents := baseline + amplitude*math.Sin(2*math.Pi*float64(t)/period)
		eventsThisSecond := int(math.Max(avgEvents, 0))
		totalEvents := eventsThisSecond
		inferenced := 0

		var wg sync.WaitGroup
		// Dispatch events concurrently.
		for i := 0; i < eventsThisSecond; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Acquire a pod slot.
				<-podSemaphore
				// Ensure to release pod slot when done.
				defer func() {
					podSemaphore <- struct{}{}
				}()
				// Wait for token availability with a timeout.
				ok := rateLimiter.Wait("basic", 20*time.Millisecond)
				if ok {
					// Simulate processing (for example, inference delay).
					time.Sleep(processingDelay + time.Duration(rand.Intn(5))*time.Millisecond)
					// Count as a successfully processed (inferenced) event.
					mu.Lock()
					inferenced++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		// Adaptive rate adjustment based on ratio of inferenced events.
		ratio := float64(inferenced) / float64(totalEvents)
		if ratio > 0.9 {
			rateLimiter.IncreaseRate()
		} else {
			rateLimiter.ReduceRate()
		}

		// Save per-second metrics.
		mu.Lock()
		seconds = append(seconds, float64(t))
		totalEventsArr = append(totalEventsArr, float64(totalEvents))
		inferencedEventsArr = append(inferencedEventsArr, float64(inferenced))
		maxCapacityArr = append(maxCapacityArr, float64(gpuMaxCapacity))
		mu.Unlock()

		log.Printf("Second %d: Total events = %d, Inferenced = %d, Current Rate: %.2f",
			t, totalEvents, inferenced, rateLimiter.GetRate())
		time.Sleep(simInterval)
	}
	elapsed := time.Since(simStart)
	log.Printf("Simulation complete in %v", elapsed)

	err := plotResults(seconds, totalEventsArr, inferencedEventsArr, maxCapacityArr)
	if err != nil {
		log.Fatalf("Error plotting results: %v", err)
	}
	log.Println("Graph generated: simulation_results.png")
}

// plotResults plots three lines: GPU capacity, total events per second, and inferenced events per second.
func plotResults(x, total, inferenced, capacity []float64) error {
	p := plot.New()
	p.Title.Text = "Dynamic Rate Limiter Simulation Results"
	p.X.Label.Text = "Time (seconds)"
	p.Y.Label.Text = "Events per Second"

	totalPoints := make(plotter.XYs, len(x))
	inferencedPoints := make(plotter.XYs, len(x))
	capacityPoints := make(plotter.XYs, len(x))
	for i := range x {
		totalPoints[i].X = x[i]
		totalPoints[i].Y = total[i]
		inferencedPoints[i].X = x[i]
		inferencedPoints[i].Y = inferenced[i]
		capacityPoints[i].X = x[i]
		capacityPoints[i].Y = capacity[i]
	}

	// Define custom colors.
	blue := color.RGBA{R: 0, G: 0, B: 255, A: 255}
	yellow := color.RGBA{R: 255, G: 255, B: 0, A: 255}
	green := color.RGBA{R: 0, G: 128, B: 0, A: 255}

	// Plot GPU Capacity (blue).
	capacityLine, err := plotter.NewLine(capacityPoints)
	if err != nil {
		return err
	}
	capacityLine.LineStyle.Width = vg.Points(2)
	capacityLine.LineStyle.Color = blue
	p.Add(capacityLine)
	p.Legend.Add("GPU Capacity", capacityLine)

	// Plot Total Events (yellow).
	totalLine, err := plotter.NewLine(totalPoints)
	if err != nil {
		return err
	}
	totalLine.LineStyle.Width = vg.Points(2)
	totalLine.LineStyle.Color = yellow
	p.Add(totalLine)
	p.Legend.Add("Total Events", totalLine)

	// Plot Inferenced Events (green).
	inferencedLine, err := plotter.NewLine(inferencedPoints)
	if err != nil {
		return err
	}
	inferencedLine.LineStyle.Width = vg.Points(2)
	inferencedLine.LineStyle.Color = green
	p.Add(inferencedLine)
	p.Legend.Add("Inferenced Events", inferencedLine)

	p.Legend.Top = true

	timestamp := time.Now().Format(time.RFC3339)
	if err := p.Save(8*vg.Inch, 4*vg.Inch, fmt.Sprintf("./output/simulation_results_%s.png", timestamp)); err != nil {
		return err
	}
	return nil
}
