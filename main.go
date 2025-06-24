package main

import (
	"context"
	"image/color"
	"log"
	"math"
	"sync"
	"time"

	"github.com/iamharshvirani/simulate-ratelimiting/ratelimiter"
	"github.com/sirupsen/logrus"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

// main runs a simulation of the DynamicRateLimiter to demonstrate its behavior and
// plot the results. It simulates a sine wave of events per second, and after each
// second, it adjusts the rate limiter based on the ratio of successfully inferenced
// events to total events. The results are plotted to a PNG file.
func main() {
	ctx := context.Background()
	simDuration := 5           // seconds
	simInterval := time.Second // per-second data aggregation
	gpuMaxCapacity := 125      // maximum fps of GPU

	l := logrus.New()
	l.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	logger := &ratelimiter.SimpleLogger{SimpleLogger: l}

	rateLimiter := ratelimiter.NewDynamicRateLimiter(
		ctx,
		logger,
		"simLimiter",
		1,
		10.0,
		1.0,
		20.0,
		0.5,
		1.0)

	// We'll simulate one-second buckets of events and adjust after each second.
	// Using a sine wave to fluctuate event rate.
	baseline := 50.0
	amplitude := 100.0
	period := 60.0 // one minute period

	var mu sync.Mutex
	// Slices to hold per-second simulation data.
	var seconds []float64
	var totalEventsArr []float64
	var inferencedEventsArr []float64
	var maxCapacityArr []float64

	log.Println("Starting simulation...")
	startTime := time.Now()
	for t := 0; t < simDuration; t++ {
		// Calculate events per second.
		avgEvents := baseline + amplitude*math.Sin(2*math.Pi*float64(t)/period)
		eventsThisSecond := int(math.Max(avgEvents, 0))
		totalEvents := eventsThisSecond
		inferenced := 0

		// Process each event through the rate limiter.
		for i := 0; i < eventsThisSecond; i++ {
			ok := rateLimiter.Wait("basic", 20*time.Millisecond)
			if ok {
				inferenced++
			}
		}

		// Adaptive adjustment.
		ratio := float64(inferenced) / float64(totalEvents)
		if ratio < 0.8 {
			rateLimiter.ReduceRate()
		} else if ratio > 0.95 {
			rateLimiter.IncreaseRate()
		}

		// Save per-second data.
		mu.Lock()
		seconds = append(seconds, float64(t))
		totalEventsArr = append(totalEventsArr, float64(totalEvents))
		inferencedEventsArr = append(inferencedEventsArr, float64(inferenced))
		maxCapacityArr = append(maxCapacityArr, float64(gpuMaxCapacity))
		mu.Unlock()

		// Print progress every 10 seconds
		if t%10 == 0 {
			log.Printf("Simulation progress: %d/%d seconds", t, simDuration)
		}

		// Sleep to simulate a real second.
		time.Sleep(simInterval)
	}
	elapsed := time.Since(startTime)
	log.Printf("Simulation complete in %v", elapsed)

	// Plot the results using Gonum/plot.
	err := plotResults(seconds, totalEventsArr, inferencedEventsArr, maxCapacityArr)
	if err != nil {
		log.Fatalf("Error plotting results: %v", err)
	}
	log.Println("Graph generated: simulation_results.png")
}

// plotResults creates a graph with three lines: maximum capacity, total events, and successfully inferenced events.
func plotResults(x, total, inferenced, capacity []float64) error {
	p := plot.New()
	p.Title.Text = "Dynamic Rate Limiter Simulation Results"
	p.X.Label.Text = "Time (seconds)"
	p.Y.Label.Text = "Events per Second"

	// Create plotter.XYs for each line.
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

	err := plotutil.AddLinePoints(p,
		"Total Events", totalPoints,
		"Inferenced Events", inferencedPoints,
		"GPU Capacity", capacityPoints)
	if err != nil {
		return err
	}

	// Customize GPU capacity line style: dashed red line.
	if lp, err := plotter.NewLine(capacityPoints); err == nil {
		lp.LineStyle.Color = color.RGBA{R: 255, A: 255}
		lp.LineStyle.Dashes = []vg.Length{5, 5}
		p.Add(lp)
	}

	// Save the plot as a PNG file.
	if err := p.Save(8*vg.Inch, 4*vg.Inch, "simulation_results.png"); err != nil {
		return err
	}
	return nil
}
