package benchmark

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/ollama/ollama/api"
)

// ServerURL is the default Ollama server URL for benchmarking
const serverURL = "http://127.0.0.1:11434"

// Command line flags
var modelFlag string

func init() {
	flag.StringVar(&modelFlag, "m", "", "Name of the model to benchmark")
	flag.Lookup("m").DefValue = "model"
}

// getModel returns the model name from flags, failing the test if not set
func getModel(b *testing.B) string {
	if modelFlag == "" {
		b.Fatal("Error: -m flag is required for benchmark tests")
	}
	return modelFlag
}

type TestCase struct {
	name      string
	prompt    string
	maxTokens int
}

// runGenerateBenchmark contains the common generate and metrics logic
func runGenerateBenchmark(b *testing.B, ctx context.Context, client *api.Client, req *api.GenerateRequest) {
	start := time.Now()
	var ttft time.Duration
	var metrics api.Metrics

	err := client.Generate(ctx, req, func(resp api.GenerateResponse) error {
		if ttft == 0 && resp.Response != "" {
			ttft = time.Since(start)
		}
		if resp.Done {
			metrics = resp.Metrics
		}
		return nil
	})

	// Report custom metrics as part of the benchmark results
	b.ReportMetric(float64(ttft.Milliseconds()), "ttft_ms")
	b.ReportMetric(float64(metrics.LoadDuration.Milliseconds()), "load_ms")

	// Token throughput metrics
	promptThroughput := float64(metrics.PromptEvalCount) / metrics.PromptEvalDuration.Seconds()
	genThroughput := float64(metrics.EvalCount) / metrics.EvalDuration.Seconds()
	b.ReportMetric(promptThroughput, "prompt_tok/s")
	b.ReportMetric(genThroughput, "gen_tok/s")

	// Token counts
	b.ReportMetric(float64(metrics.PromptEvalCount), "prompt_tokens")
	b.ReportMetric(float64(metrics.EvalCount), "gen_tokens")
	if err != nil {
		b.Fatal(err)
	}
}

// BenchmarkColdStart runs benchmarks with model loading from cold state
func BenchmarkColdStart(b *testing.B) {
	client := setup(b)
	tests := []TestCase{
		{"short_prompt", "Write a long story", 100},
		{"medium_prompt", "Write a detailed economic analysis", 500},
		{"long_prompt", "Write a comprehensive AI research paper", 1000},
	}
	m := getModel(b)

	for _, tt := range tests {
		b.Run(fmt.Sprintf("%s/cold/%s", m, tt.name), func(b *testing.B) {
			ctx := context.Background()

			// Set number of tokens as our throughput metric
			b.SetBytes(int64(tt.maxTokens))

			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				// Ensure model is unloaded before each iteration
				unload(client, m, b)
				b.StartTimer()

				req := &api.GenerateRequest{
					Model:   m,
					Prompt:  tt.prompt,
					Options: map[string]interface{}{"num_predict": tt.maxTokens, "temperature": 0.1},
				}

				runGenerateBenchmark(b, ctx, client, req)
			}
		})
	}
}

// BenchmarkWarmStart runs benchmarks with pre-loaded model
func BenchmarkWarmStart(b *testing.B) {
	client := setup(b)
	tests := []TestCase{
		{"short_prompt", "Write a long story", 100},
		{"medium_prompt", "Write a detailed economic analysis", 500},
		{"long_prompt", "Write a comprehensive AI research paper", 1000},
	}
	m := getModel(b)

	for _, tt := range tests {
		b.Run(fmt.Sprintf("%s/warm/%s", m, tt.name), func(b *testing.B) {
			ctx := context.Background()

			// Pre-warm the model
			warmup(client, m, tt.prompt, b)

			// Set number of tokens as our throughput metric
			b.SetBytes(int64(tt.maxTokens))

			b.ResetTimer()
			for range b.N {
				req := &api.GenerateRequest{
					Model:   m,
					Prompt:  tt.prompt,
					Options: map[string]interface{}{"num_predict": tt.maxTokens, "temperature": 0.1},
				}

				runGenerateBenchmark(b, ctx, client, req)
			}
		})
	}
}

// setup verifies server and model availability
func setup(b *testing.B) *api.Client {
	resp, err := http.Get(serverURL + "/api/version")
	if err != nil {
		b.Fatalf("Server unavailable: %v", err)
	}
	defer resp.Body.Close()

	client := api.NewClient(mustParse(serverURL), http.DefaultClient)
	if _, err := client.Show(context.Background(), &api.ShowRequest{Model: getModel(b)}); err != nil {
		b.Fatalf("Model unavailable: %v", err)
	}

	return client
}

// warmup ensures the model is loaded and warmed up
func warmup(client *api.Client, model string, prompt string, b *testing.B) {
	for range 3 {
		err := client.Generate(
			context.Background(),
			&api.GenerateRequest{
				Model:   model,
				Prompt:  prompt,
				Options: map[string]interface{}{"num_predict": 50, "temperature": 0.1},
			},
			func(api.GenerateResponse) error { return nil },
		)
		if err != nil {
			b.Logf("Error during model warm-up: %v", err)
		}
	}
}

// unload forces model unloading using KeepAlive: 0 parameter
func unload(client *api.Client, model string, b *testing.B) {
	req := &api.GenerateRequest{
		Model:     model,
		KeepAlive: &api.Duration{Duration: 0},
	}
	if err := client.Generate(context.Background(), req, func(api.GenerateResponse) error { return nil }); err != nil {
		b.Logf("Unload error: %v", err)
	}
	time.Sleep(1 * time.Second)
}

func mustParse(rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return u
}
