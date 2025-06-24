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
	"gonum.org/v1/plot/vg"
)

// main runs a simulation of the DynamicRateLimiter to demonstrate its behavior and
// plot the results. It simulates a sine wave of events per second, and after each
// second, it adjusts the rate limiter based on the ratio of successfully inferenced
// events to total events. The results are plotted to a PNG file.
func main() {
	ctx := context.Background()
	simDuration := 300         // seconds
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

	if err := p.Save(8*vg.Inch, 4*vg.Inch, "simulation_results.png"); err != nil {
		return err
	}
	return nil
}
