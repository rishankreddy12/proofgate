// Command cacheeval runs offline semantic cache calibration over evaluation datasets (such as QQP).
//
// Usage:
//
//	cacheeval --dataset bench/datasets/qqp-replay.jsonl --n 500 --output bench/results/cache_curve.json
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/proofgate/proofgate/internal/proof"
)

type ReplayItem struct {
	ID     string `json:"id"`
	Group  string `json:"group"`
	Phase  int    `json:"phase"`
	Prompt string `json:"prompt"`
}

type IndexedDoc struct {
	ID        string
	Group     string
	Prompt    string
	Answer    string
	Embedding []float32
}

func cosineSim(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func main() {
	datasetPath := flag.String("dataset", "bench/datasets/qqp-replay.jsonl", "path to QQP jsonl dataset")
	limitPairs := flag.Int("n", 500, "number of pairs to evaluate (0 = all)")
	targetFHR := flag.Float64("target-fhr", 0.01, "target false-hit rate upper bound")
	outputPath := flag.String("output", "bench/results/cache_curve.json", "output path for curve JSON")
	flag.Parse()

	f, err := os.Open(*datasetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open dataset: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	var phase1 []ReplayItem
	var phase2 []ReplayItem

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var item ReplayItem
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			continue
		}
		if item.Phase == 1 {
			phase1 = append(phase1, item)
		} else if item.Phase == 2 {
			phase2 = append(phase2, item)
		}
	}

	total := min(len(phase1), len(phase2))
	if *limitPairs > 0 && *limitPairs < total {
		total = *limitPairs
	}

	fmt.Printf("Evaluating %d QQP pairs using deterministic embeddings...\n", total)

	// 1. Index phase 1
	indexed := make([]IndexedDoc, 0, total)
	for i := 0; i < total; i++ {
		p1 := phase1[i]
		emb := mockllm.HashEmbedding(p1.Prompt, 128)
		indexed = append(indexed, IndexedDoc{
			ID:        p1.ID,
			Group:     p1.Group,
			Prompt:    p1.Prompt,
			Answer:    "Answer for " + p1.Prompt,
			Embedding: emb,
		})
	}

	// 2. Query phase 2 and evaluate matches
	var sims []float64
	var labeled []proof.LabeledPoint

	for i := 0; i < total; i++ {
		p2 := phase2[i]
		emb2 := mockllm.HashEmbedding(p2.Prompt, 128)

		var bestSim float64 = -1
		var bestDoc IndexedDoc
		for _, doc := range indexed {
			sim := cosineSim(emb2, doc.Embedding)
			if sim > bestSim {
				bestSim = sim
				bestDoc = doc
			}
		}

		isDuplicate := p2.Group == bestDoc.Group && !strings.HasPrefix(p2.Group, "n")
		sims = append(sims, bestSim)
		labeled = append(labeled, proof.LabeledPoint{
			Similarity: bestSim,
			Acceptable: isDuplicate,
			Rater:      "dataset",
		})
	}

	// 3. Compute False-Hit Curve
	recommended, curve := proof.TuneCacheFromData(sims, labeled, *targetFHR)

	// 4. Print ASCII Curve Table
	fmt.Println("\nFalse-Hit Rate Calibration Curve:")
	fmt.Println("+-----------+----------+----------------+--------------+-------+")
	fmt.Println("| Threshold | Hit Rate | False Hit Rate | Wilson Upper |   N   |")
	fmt.Println("+-----------+----------+----------------+--------------+-------+")
	for _, pt := range curve {
		// Sample rows or every step
		fmt.Printf("|   %4.2f    |  %6.2f%% |     %6.2f%%    |    %6.2f%%   | %5d |\n",
			pt.Threshold, pt.HitRate*100, pt.FalseHitRate*100, pt.WilsonUpper*100, pt.SampleCount)
	}
	fmt.Println("+-----------+----------+----------------+--------------+-------+")
	fmt.Printf("\nRecommended Threshold (Target FHR <= %.2f%%): %.2f\n", *targetFHR*100, recommended)

	// 5. Output JSON file
	if *outputPath != "" {
		if dir := filepath.Dir(*outputPath); dir != "" {
			_ = os.MkdirAll(dir, 0755)
		}
		outData := map[string]any{
			"dataset":               *datasetPath,
			"pairs":                 total,
			"target_false_hit_rate": *targetFHR,
			"recommended_threshold": recommended,
			"curve":                 curve,
		}
		b, err := json.MarshalIndent(outData, "", "  ")
		if err == nil {
			_ = os.WriteFile(*outputPath, b, 0644)
			fmt.Printf("Wrote calibration curve results to %s\n", *outputPath)
		}
	}
}
