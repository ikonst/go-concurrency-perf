package main

import (
	"context"
	"fmt"
	"github.com/montanaflynn/stats"
	"golang.org/x/sync/semaphore"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	"image/color"
	"strings"
	"time"
)

type WorkResult struct {
	timeTaken time.Duration
	output    string
}

func doCpuWork(workTime time.Duration, name string, sb *strings.Builder) {
	start := time.Now()
	var end time.Time
	var duration time.Duration
	for true {
		end = time.Now()
		duration = end.Sub(start)
		if duration >= workTime {
			break
		}
	}
	sb.WriteString(fmt.Sprintf("[%s] %s: + %v CPU time\n", start.Format(time.StampMicro), name, workTime))
	sb.WriteString(fmt.Sprintf("[%s] %s: - %v CPU work took %v\n", end.Format(time.StampMicro), name, workTime, duration))
}

func doNetworkWork(networkTime time.Duration, name string, sb *strings.Builder) {
	start := time.Now()
	sb.WriteString(fmt.Sprintf("[%s] %s: + %v network time\n", start.Format(time.StampMicro), name, networkTime))
	time.Sleep(networkTime) // Simulate Network Work by calling sleep
	end := time.Now()
	duration := end.Sub(start)
	sb.WriteString(fmt.Sprintf("[%s] %s: - %v Network time took %v\n", end.Format(time.StampMicro), name, networkTime, duration))
}

func doWork(workTime time.Duration, networkTime time.Duration, splits int, name string, sb *strings.Builder) time.Duration {
	start := time.Now()
	doCpuWork(workTime/time.Duration(splits+1), name, sb)
	for i := 0; i < splits; i++ {
		doNetworkWork(networkTime/time.Duration(splits), name, sb)
		doCpuWork(workTime/time.Duration(splits+1), name, sb)
	}
	return time.Since(start)
}

type BenchmarkResult struct {
	WorkTime        time.Duration
	NetworkTime     time.Duration
	Iterations      int
	NumCoroutines   int64
	ThroughputRps   float64
	Speedup         float64
	CpuUtilization  float64
	ResponseTimesMs []float64
	LongestRequest  string
}

func (b BenchmarkResult) ResponseTimesPercentile(pct float64) float64 {
	val, _ := stats.Percentile(b.ResponseTimesMs, pct)
	return val
}

func runBenchmark(workTime, networkTime time.Duration, numGreenThreads int64, splits int, baselineIterations int, iterations int) BenchmarkResult {
	var start time.Time

	// Compute baseline
	start = time.Now()
	var dummySb strings.Builder
	for x := 0; x < baselineIterations; x++ {
		doWork(workTime, networkTime, splits, fmt.Sprintf("Request %d", x), &dummySb)
	}
	baselineDuration := time.Since(start)

	// Run benchmark
	start = time.Now()
	c := make(chan WorkResult, iterations)
	sem := semaphore.NewWeighted(numGreenThreads)
	ctx := context.Background()

	for x := 0; x < iterations; x++ {
		err := sem.Acquire(ctx, 1)
		if err != nil {
			panic(err)
		}
		go func(x int) {
			var sb strings.Builder
			timeTaken := doWork(workTime, networkTime, splits, fmt.Sprintf("Request %d", x), &sb)
			c <- WorkResult{
				timeTaken: timeTaken,
				output:    sb.String(),
			}
			sem.Release(1)
		}(x)
	}

	var responseTimesMs []float64
	var longestRequest WorkResult
	for result := range c {
		if result.timeTaken > longestRequest.timeTaken {
			longestRequest = result
		}
		responseTimesMs = append(responseTimesMs, float64(result.timeTaken)/float64(time.Millisecond))
		if len(responseTimesMs) == iterations {
			close(c)
		}
	}

	totalDuration := time.Since(start)
	// avgBaselineDurationS := baselineDurationS / float64(baselineIterations)
	baselineRps := float64(baselineIterations) / baselineDuration.Seconds()
	resultRps := float64(iterations) / totalDuration.Seconds()
	maxRps := 1 / workTime.Seconds()

	return BenchmarkResult{
		WorkTime:        workTime,
		NetworkTime:     networkTime,
		Iterations:      iterations,
		NumCoroutines:   numGreenThreads,
		ThroughputRps:   resultRps,
		Speedup:         resultRps / baselineRps,
		CpuUtilization:  resultRps * 100.0 / maxRps,
		ResponseTimesMs: responseTimesMs,
		LongestRequest:  longestRequest.output,
	}
}

func outputBenchmarkResult(result BenchmarkResult, printDetails bool) {
	fmt.Printf("%v CPU/%v Network per request (%d requests with %d co-routines)\n", result.WorkTime, result.NetworkTime, result.Iterations, result.NumCoroutines)
	fmt.Printf("\tThroughput: %.2f rps (%.2fX Speedup)\n", result.ThroughputRps, result.Speedup)
	fmt.Printf("\tCPU Utilization: %.2f%%\n", result.CpuUtilization)
	for _, pct := range []float64{50, 95, 99} {
		fmt.Printf("\tp%.0f: %.2fms\n", pct, result.ResponseTimesPercentile(pct))
	}
	if printDetails {
		fmt.Println("=========================================")
		fmt.Println("Longest Request:")
		for _, line := range strings.Split(result.LongestRequest, "\n") {
			fmt.Println("\t" + line)
		}
	}

	saveHistogram(result)
}

func saveHistogram(result BenchmarkResult) {
	p := plot.New()
	hist, err := plotter.NewHist(plotter.Values(result.ResponseTimesMs), 20)
	if err != nil {
		panic(err)
	}
	p.Add(hist)
	err = p.Save(4*vg.Inch, 4*vg.Inch, "hist.png")
	if err != nil {
		panic(err)
	}
}

func throughputBenchmark() {
	var results []BenchmarkResult
	for _, numGreenThreads := range []int64{1, 3, 5, 7, 9, 11, 13, 15, 17, 19, 21, 23} {
		result := runBenchmark(
			time.Duration(5)*time.Millisecond,
			time.Duration(55)*time.Millisecond,
			numGreenThreads, 5, 100, 100)
		outputBenchmarkResult(result, true)
		results = append(results, result)
	}

	plotThroughput(results)
	plotLatency(results)
}

func plotThroughput(results []BenchmarkResult) {
	plt := plot.New()
	plt.Title.Text = "Throughput Increase vs. Number of Co-Routines"
	plt.X.Label.Text = "Number of Co-Routines"
	plt.Y.Label.Text = "Speedup"
	plt.Y.Min = 0

	var pts plotter.XYs
	for _, result := range results {
		pts = append(pts, plotter.XY{X: float64(result.NumCoroutines), Y: result.Speedup})
	}
	line, err := plotter.NewLine(pts)
	if err != nil {
		panic(err)
	}
	line.LineStyle.Width = vg.Points(3)
	line.LineStyle.Color = color.RGBA{B: 255, A: 255}
	plt.Add(line)

	plt.Legend.Add("line", line)
	err = plt.Save(4*vg.Inch, 4*vg.Inch, "throughput_vs_coroutines.png")
	if err != nil {
		panic(err)
	}
}

func plotLatency(results []BenchmarkResult) {
	plt := plot.New()
	plt.Title.Text = "Throughput Increase vs. Number of Co-Routines"
	plt.X.Label.Text = "Number of Co-Routines"
	plt.Y.Label.Text = "Latency"
	plt.Y.Min = 0

	for _, percentile := range []float64{50, 95, 99} {
		var pts plotter.XYs
		for _, result := range results {
			latency := result.ResponseTimesPercentile(percentile)
			if latency > plt.Y.Max {
				plt.Y.Max = latency + 20
			}
			pts = append(pts, plotter.XY{X: float64(result.NumCoroutines), Y: latency})
		}

		line, _ := plotter.NewLine(pts)
		line.LineStyle.Width = vg.Points(1)
		line.LineStyle.Color = map[float64]color.RGBA{
			50: {R: 255, A: 255},
			95: {G: 255, A: 255},
			99: {B: 255, A: 255},
		}[percentile]
		plt.Add(line)
		plt.Legend.Add(fmt.Sprintf("p%.0f response time", percentile), line)
	}

	err := plt.Save(4*vg.Inch, 4*vg.Inch, "latency_vs_coroutines.png")
	if err != nil {
		panic(err)
	}
}

func main() {
	throughputBenchmark()
}
