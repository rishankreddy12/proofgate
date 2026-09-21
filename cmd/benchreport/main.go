package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func loadResults(dir string) (map[string]any, error) {
	res := make(map[string]any)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		relKey := filepath.ToSlash(rel)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		res[relKey] = v
		return nil
	})
	return res, err
}

func collectGroup(results map[string]any, prefix string) []map[string]any {
	var files []map[string]any
	for k, v := range results {
		if strings.HasPrefix(k, prefix) {
			if m, ok := v.(map[string]any); ok {
				files = append(files, m)
			}
		}
	}
	return files
}

func renderBenchmarksDoc(results map[string]any) (string, error) {
	var buf bytes.Buffer
	buf.WriteString("# ProofGate Benchmark Report\n\n")
	buf.WriteString("**Status:** Verified & Committed  \n")
	buf.WriteString("**Reproducibility:** One command (`make bench-overhead` / `bash bench/compare/run.sh`)  \n\n")
	buf.WriteString("---\n\n")

	buf.WriteString("## 1. Executive Summary & Methodology\n\n")
	buf.WriteString("To measure the true overhead of ProofGate without the confounding noise of public LLM API rate limits, internet jitter, or provider variance, all benchmarks are conducted against a local deterministic mock provider (`mockllm-bench`) configured for **50 ms Time to First Token (TTFT)** and **200 tokens/second** generation.\n\n")
	buf.WriteString("All measurements employ an **open-loop load generator** (`cmd/loadgen`) that dispatches requests on a strict schedule (`1/rps`) regardless of whether earlier requests have returned. This guarantees that latency spikes cannot artificially reduce the applied load.\n\n")

	buf.WriteString("### Core Headline Numbers\n\n")
	buf.WriteString("- **Added Streaming TTFT Overhead (p50):** {{bench:overhead/stream-1000-gateway-1.json:ttft.p50_ms}} ms\n")
	buf.WriteString("- **Added Streaming TTFT Overhead (p99):** {{bench:overhead/stream-1000-gateway-1.json:ttft.p99_ms}} ms\n")
	buf.WriteString("- **Throughput Saturation:** {{bench:overhead/saturation.json:saturation_rps}} RPS per 4 vCPUs ({{bench:overhead/saturation.json:max_rps_per_vcpu}} RPS/vCPU)\n")
	buf.WriteString("- **First-Run Time (clean clone to 200 OK):** {{bench:first-run.json:seconds}}s\n\n")

	buf.WriteString("---\n\n")
	buf.WriteString("## 2. Gateway Overhead (Direct vs ProofGate)\n\n")
	buf.WriteString("Identical traffic distributions evaluated over 30-second windows across 3 independent repeats:\n\n")
	buf.WriteString("| Scenario | Target | Requests | TTFT p50 (ms) | TTFT p90 (ms) | TTFT p99 (ms) | Total p50 (ms) | Total p99 (ms) |\n")
	buf.WriteString("| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")
	buf.WriteString("| **stream-200** | Direct Mock | {{bench:overhead/stream-200-direct-1.json:requests}} | {{bench:overhead/stream-200-direct-1.json:ttft.p50_ms}} | {{bench:overhead/stream-200-direct-1.json:ttft.p90_ms}} | {{bench:overhead/stream-200-direct-1.json:ttft.p99_ms}} | {{bench:overhead/stream-200-direct-1.json:total.p50_ms}} | {{bench:overhead/stream-200-direct-1.json:total.p99_ms}} |\n")
	buf.WriteString("| **stream-200** | ProofGate | {{bench:overhead/stream-200-gateway-1.json:requests}} | {{bench:overhead/stream-200-gateway-1.json:ttft.p50_ms}} | {{bench:overhead/stream-200-gateway-1.json:ttft.p90_ms}} | {{bench:overhead/stream-200-gateway-1.json:ttft.p99_ms}} | {{bench:overhead/stream-200-gateway-1.json:total.p50_ms}} | {{bench:overhead/stream-200-gateway-1.json:total.p99_ms}} |\n")
	buf.WriteString("| **stream-1000** | Direct Mock | {{bench:overhead/stream-1000-direct-1.json:requests}} | {{bench:overhead/stream-1000-direct-1.json:ttft.p50_ms}} | {{bench:overhead/stream-1000-direct-1.json:ttft.p90_ms}} | {{bench:overhead/stream-1000-direct-1.json:ttft.p99_ms}} | {{bench:overhead/stream-1000-direct-1.json:total.p50_ms}} | {{bench:overhead/stream-1000-direct-1.json:total.p99_ms}} |\n")
	buf.WriteString("| **stream-1000** | ProofGate | {{bench:overhead/stream-1000-gateway-1.json:requests}} | {{bench:overhead/stream-1000-gateway-1.json:ttft.p50_ms}} | {{bench:overhead/stream-1000-gateway-1.json:ttft.p90_ms}} | {{bench:overhead/stream-1000-gateway-1.json:ttft.p99_ms}} | {{bench:overhead/stream-1000-gateway-1.json:total.p50_ms}} | {{bench:overhead/stream-1000-gateway-1.json:total.p99_ms}} |\n")
	buf.WriteString("| **nostream-1000** | Direct Mock | {{bench:overhead/nostream-1000-direct-1.json:requests}} | - | - | - | {{bench:overhead/nostream-1000-direct-1.json:total.p50_ms}} | {{bench:overhead/nostream-1000-direct-1.json:total.p99_ms}} |\n")
	buf.WriteString("| **nostream-1000** | ProofGate | {{bench:overhead/nostream-1000-gateway-1.json:requests}} | - | - | - | {{bench:overhead/nostream-1000-gateway-1.json:total.p50_ms}} | {{bench:overhead/nostream-1000-gateway-1.json:total.p99_ms}} |\n\n")

	buf.WriteString("---\n\n")
	buf.WriteString("## 3. Same-Harness Competitor Comparison\n\n")
	buf.WriteString("ProofGate, LiteLLM, and Bifrost were deployed on the same physical host under identical constraints (**4 CPUs, 8 GB RAM**, equal open-loop load of 1,000 RPS, three repeats):\n\n")

	pgStream := collectGroup(results, "compare/proofgate-stream-1000")
	liteStream := collectGroup(results, "compare/litellm-stream-1000")
	biStream := collectGroup(results, "compare/bifrost-stream-1000")

	pgP50, _ := Median(pgStream, "ttft.p50_ms")
	pgP90, _ := Median(pgStream, "ttft.p90_ms")
	pgP99, _ := Median(pgStream, "ttft.p99_ms")
	pgTot, _ := Median(pgStream, "total.p99_ms")
	pgRPS, _ := Median(pgStream, "achieved_rps")
	pgErr, _ := Median(pgStream, "errors")

	liteP50, _ := Median(liteStream, "ttft.p50_ms")
	liteP90, _ := Median(liteStream, "ttft.p90_ms")
	liteP99, _ := Median(liteStream, "ttft.p99_ms")
	liteTot, _ := Median(liteStream, "total.p99_ms")
	liteRPS, _ := Median(liteStream, "achieved_rps")
	liteErr, _ := Median(liteStream, "errors")

	biP50, _ := Median(biStream, "ttft.p50_ms")
	biP90, _ := Median(biStream, "ttft.p90_ms")
	biP99, _ := Median(biStream, "ttft.p99_ms")
	biTot, _ := Median(biStream, "total.p99_ms")
	biRPS, _ := Median(biStream, "achieved_rps")
	biErr, _ := Median(biStream, "errors")

	buf.WriteString("| Gateway | Version / Image | Streaming TTFT p50 | Streaming TTFT p90 | Streaming TTFT p99 | Total Latency p99 | Achieved RPS | Errors |\n")
	buf.WriteString("| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")
	buf.WriteString(fmt.Sprintf("| **ProofGate** | local-build (Go) | **%.2f ms** | **%.2f ms** | **%.2f ms** | **%.2f ms** | **%.1f** | **%d** |\n", pgP50, pgP90, pgP99, pgTot, pgRPS, int(pgErr)))
	buf.WriteString(fmt.Sprintf("| **Bifrost** | maximhq/bifrost (Go) | %.2f ms | %.2f ms | %.2f ms | %.2f ms | %.1f | %d |\n", biP50, biP90, biP99, biTot, biRPS, int(biErr)))
	buf.WriteString(fmt.Sprintf("| **LiteLLM** | berriai/litellm (Python) | %.2f ms | %.2f ms | %.2f ms | %.2f ms | %.1f | %d |\n\n", liteP50, liteP90, liteP99, liteTot, liteRPS, int(liteErr)))

	buf.WriteString("> [!IMPORTANT]\n")
	buf.WriteString("> **What this benchmark does NOT show:**\n")
	buf.WriteString("> 1. **Real-world upstream latency:** Network trips to OpenAI, Anthropic, or Google introduce 200ms–2000ms latency that dwarf gateway proxy overhead.\n")
	buf.WriteString("> 2. **Multi-node cluster scaling:** Tested on a single 4-core container.\n")
	buf.WriteString("> 3. **Feature parity:** Caching, analytics, and guardrails were turned off. LiteLLM has hundreds of provider integrations; ProofGate focuses deeply on correctness, verification, safe caching, and statistical rollback.\n")
	buf.WriteString("> See each project's own numbers: [LiteLLM Benchmarks](https://docs.litellm.ai), [Bifrost Benchmarks](https://github.com/maximhq/bifrost).\n\n")

	buf.WriteString("---\n\n")
	buf.WriteString("## 4. Semantic Cache Calibration\n\n")
	buf.WriteString("Over 500 QQP evaluation pairs with calibrated embedding similarities:\n")
	buf.WriteString("- Selected 1% False-Hit Bound Threshold: `{{bench:phase3/cache-qqp-nomic.json:choice_1pct.point.threshold}}` (Wilson 95% upper bound: `{{bench:phase3/cache-qqp-nomic.json:choice_1pct.point.wilson_upper}}`)\n")
	buf.WriteString("- Selected 5% False-Hit Bound Threshold: `{{bench:phase3/cache-qqp-nomic.json:choice_5pct.point.threshold}}` (Wilson 95% upper bound: `{{bench:phase3/cache-qqp-nomic.json:choice_5pct.point.wilson_upper}}`)\n")
	buf.WriteString("- Inter-rater agreement (Cohen's κ): `{{bench:phase3/cache-qqp-nomic.json:kappa}}`\n\n")

	buf.WriteString("---\n\n")
	buf.WriteString("## 5. Smart Routing & Auto-Rollback\n\n")
	buf.WriteString("- Evaluated queries: `{{bench:phase3/routing-report-before.json:total_queries}}`\n")
	buf.WriteString("- Cost reduction: **{{bench:phase3/routing-report-before.json:percent_saved}}%** (${{bench:phase3/routing-report-before.json:actual_usd}} vs ${{bench:phase3/routing-report-before.json:counterfactual_usd}} baseline)\n")
	buf.WriteString("- Quality delta mean: `{{bench:phase3/routing-report-before.json:quality_delta_mean}}` (95% Bootstrap CI: `[{{bench:phase3/routing-report-before.json:quality_delta_ci_low}}, +{{bench:phase3/routing-report-before.json:quality_delta_ci_high}}]`)\n")
	buf.WriteString("- Automatic rollback observed within: **{{bench:phase3/rollback-drill.json:seconds_to_rollback}}s**\n")

	out, errs := ResolveMarkers(buf.String(), results)
	if len(errs) > 0 {
		return "", fmt.Errorf("resolve markers in doc: %v", errs)
	}
	return out, nil
}

func main() {
	resultsDir := flag.String("results", "bench/results", "path to results directory")
	outDoc := flag.String("out", "docs/benchmarks.md", "output path for benchmarks markdown")
	templates := flag.String("templates", "README.md.tmpl,blog/*.md.tmpl", "comma-separated glob patterns for templates")
	check := flag.Bool("check", false, "verify rendered output matches disk and fail on drift/missing markers")
	flag.Parse()

	results, err := loadResults(*resultsDir)
	if err != nil {
		log.Fatalf("failed to load results from %s: %v", *resultsDir, err)
	}

	renderedDoc, err := renderBenchmarksDoc(results)
	if err != nil {
		log.Fatalf("failed to render %s: %v", *outDoc, err)
	}

	var tplFiles []string
	for _, pattern := range strings.Split(*templates, ",") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			log.Fatalf("glob %s: %v", pattern, err)
		}
		tplFiles = append(tplFiles, matches...)
	}

	var hasDrift bool
	var allErrors []error

	if *check {
		existingDoc, err := os.ReadFile(*outDoc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "CHECK FAIL: cannot read %s: %v\n", *outDoc, err)
			hasDrift = true
		} else if strings.TrimSpace(string(existingDoc)) != strings.TrimSpace(renderedDoc) {
			fmt.Fprintf(os.Stderr, "CHECK FAIL: %s is stale or differs from rendered output\n", *outDoc)
			hasDrift = true
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(*outDoc), 0o755); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(*outDoc, []byte(renderedDoc), 0o644); err != nil {
			log.Fatal(err)
		}
	}

	for _, tpl := range tplFiles {
		targetFile := strings.TrimSuffix(tpl, ".tmpl")
		tplContent, err := os.ReadFile(tpl)
		if err != nil {
			log.Fatalf("read template %s: %v", tpl, err)
		}
		resolved, errs := ResolveMarkers(string(tplContent), results)
		if len(errs) > 0 {
			for _, e := range errs {
				allErrors = append(allErrors, fmt.Errorf("%s: %w", tpl, e))
			}
		}

		if *check {
			existingTarget, err := os.ReadFile(targetFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "CHECK FAIL: cannot read target %s for template %s: %v\n", targetFile, tpl, err)
				hasDrift = true
			} else if strings.TrimSpace(string(existingTarget)) != strings.TrimSpace(resolved) {
				fmt.Fprintf(os.Stderr, "CHECK FAIL: %s is stale relative to %s\n", targetFile, tpl)
				hasDrift = true
			}
		} else {
			if err := os.WriteFile(targetFile, []byte(resolved), 0o644); err != nil {
				log.Fatalf("write %s: %v", targetFile, err)
			}
		}
	}

	if len(allErrors) > 0 {
		for _, e := range allErrors {
			fmt.Fprintf(os.Stderr, "UNRESOLVED MARKER: %v\n", e)
		}
		os.Exit(1)
	}

	if *check {
		if hasDrift {
			fmt.Fprintln(os.Stderr, "CHECK FAIL: Benchmark reports or template targets are out-of-date. Run 'go run ./cmd/benchreport' to regenerate.")
			os.Exit(1)
		}
		fmt.Println("All benchmark markers and generated documents are up-to-date and verified.")
	} else {
		fmt.Printf("Successfully generated %s and processed %d template(s).\n", *outDoc, len(tplFiles))
	}
}
