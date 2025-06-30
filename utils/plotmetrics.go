package utils

import (
	"fmt"
	"image/color"
	"time"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

// SecondMetrics holds the metrics for a single second of the simulation.
type SecondMetrics struct {
	TotalEvents          int
	InferencedEvents     int
	DroppedByRateLimiter int
	DroppedByPipeline    int
	FinalRate            float64
}

// GeneratePlotFromMetrics prepares the data and generates the plot.
func GeneratePlotFromMetrics(allMetrics []SecondMetrics, gpuMaxCapacityPerPod int, numPods int) error {
	// Prepare plotting data
	var seconds, totalEventsArr, inferencedEventsArr, maxCapacityArr, rateArr []float64
	for i, m := range allMetrics {
		seconds = append(seconds, float64(i))
		totalEventsArr = append(totalEventsArr, float64(m.TotalEvents))
		inferencedEventsArr = append(inferencedEventsArr, float64(m.InferencedEvents))
		maxCapacityArr = append(maxCapacityArr, float64(gpuMaxCapacityPerPod*numPods))
		rateArr = append(rateArr, m.FinalRate)
	}

	return PlotResults(seconds, totalEventsArr, inferencedEventsArr, maxCapacityArr, rateArr)
}

func PlotResults(x, total, inferenced, capacity, rate []float64) error {
	p := plot.New()
	p.Title.Text = "Dynamic Rate Limiter Simulation Results (Stream-Based)"
	p.X.Label.Text = "Time (seconds)"
	p.Y.Label.Text = "Events per Second"
	p.Y.Min = 0

	// Create plotters
	totalPoints := make(plotter.XYs, len(x))
	inferencedPoints := make(plotter.XYs, len(x))
	capacityPoints := make(plotter.XYs, len(x))
	ratePoints := make(plotter.XYs, len(x))
	for i := range x {
		totalPoints[i].X = x[i]
		totalPoints[i].Y = total[i]
		inferencedPoints[i].X = x[i]
		inferencedPoints[i].Y = inferenced[i]
		capacityPoints[i].X = x[i]
		capacityPoints[i].Y = capacity[i]
		ratePoints[i].X = x[i]
		ratePoints[i].Y = rate[i]
	}

	// Plot GPU Capacity (blue)
	capacityLine, _ := plotter.NewLine(capacityPoints)
	capacityLine.LineStyle.Width = vg.Points(2)
	capacityLine.LineStyle.Color = color.RGBA{R: 0, G: 0, B: 255, A: 255}
	p.Add(capacityLine)
	p.Legend.Add("GPU Capacity", capacityLine)

	// Plot Total Events (yellow)
	totalLine, _ := plotter.NewLine(totalPoints)
	totalLine.LineStyle.Width = vg.Points(2)
	totalLine.LineStyle.Dashes = []vg.Length{vg.Points(5), vg.Points(5)}
	totalLine.LineStyle.Color = color.RGBA{R: 255, G: 200, B: 0, A: 255}
	p.Add(totalLine)
	p.Legend.Add("Total Events", totalLine)

	// Plot Inferenced Events (green)
	inferencedLine, _ := plotter.NewLine(inferencedPoints)
	inferencedLine.LineStyle.Width = vg.Points(3) // Make it thicker
	inferencedLine.LineStyle.Color = color.RGBA{R: 0, G: 180, B: 0, A: 255}
	p.Add(inferencedLine)
	p.Legend.Add("Inferenced Events", inferencedLine)

	// Plot Rate Limiter Rate (red)
	rateLine, _ := plotter.NewLine(ratePoints)
	rateLine.LineStyle.Width = vg.Points(2)
	rateLine.LineStyle.Color = color.RGBA{R: 255, G: 0, B: 0, A: 255}
	p.Add(rateLine)
	p.Legend.Add("Limiter Rate", rateLine)

	p.Legend.Top = true
	p.Legend.XOffs = -10

	timestamp := time.Now().Format("20060102_150405")
	fileName := fmt.Sprintf("./output/simulation_results_%s.png", timestamp)
	if err := p.Save(10*vg.Inch, 5*vg.Inch, fileName); err != nil {
		return err
	}
	return nil
}
