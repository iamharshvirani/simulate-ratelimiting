package utils

import (
	"fmt"
	"image/color"
	"os"
	"time"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
	"gonum.org/v1/plot/vg/vgimg"
)

// SecondMetrics holds the metrics for a single second of the simulation.
type SecondMetrics struct {
	TotalEvents          int
	InferencedEvents     int
	DroppedByRateLimiter int
	DroppedByPipeline    int
	// Refill rates of each pod for this second (len == numPods)
	RatesPerPod []float64
}

// GeneratePlotFromMetrics prepares the data and generates the plot.
func GeneratePlotFromMetrics(allMetrics []SecondMetrics, gpuMaxCapacityPerPod int, numPods int) error {
	// Prepare plotting data
	var seconds, totalEventsArr, inferencedEventsArr, maxCapacityArr []float64
	podRates := make([][]float64, numPods)
	for i, m := range allMetrics {
		seconds = append(seconds, float64(i))
		totalEventsArr = append(totalEventsArr, float64(m.TotalEvents))
		inferencedEventsArr = append(inferencedEventsArr, float64(m.InferencedEvents))
		maxCapacityArr = append(maxCapacityArr, float64(gpuMaxCapacityPerPod*numPods))
		for p := 0; p < numPods; p++ {
			if len(podRates[p]) < len(allMetrics) {
				podRates[p] = append(podRates[p], m.RatesPerPod[p])
			}
		}
	}

	return PlotResults(seconds, totalEventsArr, inferencedEventsArr, maxCapacityArr, podRates)
}

func PlotResults(x, total, inferenced, capacity []float64, podRates [][]float64) error {
	// --- MAIN AGGREGATE PLOT ---
	p := plot.New()
	p.Title.Text = "Dynamic Rate Limiter Simulation Results (Stream-Based)"
	p.X.Label.Text = "Time (seconds)"
	p.Y.Label.Text = "Events per Second"
	p.Y.Min = 0

	// Create plotters
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

	p.Legend.Top = true
	p.Legend.XOffs = -10

	// --- POD-SPECIFIC RATE PLOTS ---
	podPlots := make([]*plot.Plot, len(podRates))
	for idx := range podRates {
		pp := plot.New()
		pp.Title.Text = fmt.Sprintf("Pod %d Refill Rate", idx)
		pp.X.Label.Text = "Time (seconds)"
		pp.Y.Label.Text = "Refill Rate"

		xy := make(plotter.XYs, len(x))
		for i := range x {
			xy[i].X = x[i]
			xy[i].Y = podRates[idx][i]
		}
		l, _ := plotter.NewLine(xy)
		l.LineStyle.Width = vg.Points(2)
		l.LineStyle.Color = color.RGBA{R: 255, G: uint8(30 * idx), B: uint8(200 - 20*idx), A: 255}
		pp.Add(l)
		podPlots[idx] = pp
	}

	// --- SAVE MAIN PLOT ---
	mainPlotFileName := fmt.Sprintf("./output/simulation_metrics_%s.png", time.Now().Format("20060102_150405"))
	if err := p.Save(10*vg.Inch, 5*vg.Inch, mainPlotFileName); err != nil {
		return fmt.Errorf("could not save main plot: %w", err)
	}

	// --- COMBINE AND SAVE POD PLOTS ---
	if len(podPlots) == 0 {
		return nil // No pod plots to generate
	}

	rows := len(podPlots)
	tiles := draw.Tiles{Rows: rows, Cols: 1, PadX: vg.Millimeter * 2, PadY: vg.Millimeter * 2}
	totalHeight := vg.Length(rows) * 3 * vg.Inch // 3-inch height per subplot
	img := vgimg.New(10*vg.Inch, totalHeight)
	dc := draw.New(img)

	// Draw pod plots onto the new canvas
	for i, podPlot := range podPlots {
		podPlot.Draw(tiles.At(dc, 0, i)) // Correctly draw in column 0, row i
	}

	// Save the combined pod plot image
	podPlotFileName := fmt.Sprintf("./output/pod_rates_%s.png", time.Now().Format("20060102_150405"))
	w, err := os.Create(podPlotFileName)
	if err != nil {
		return fmt.Errorf("could not create pod plot file: %w", err)
	}
	defer w.Close()

	png := vgimg.PngCanvas{Canvas: img}
	if _, err := png.WriteTo(w); err != nil {
		return fmt.Errorf("could not write pod plot png: %w", err)
	}

	return nil
}
