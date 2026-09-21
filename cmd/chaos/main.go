// Command chaos measures how long ProofGate takes to move traffic off a provider that becomes slow.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

func setMode(admin, mode string) {
	req, _ := http.NewRequest("PUT", admin+"/admin/mode", strings.NewReader(mode))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	resp.Body.Close()
}

func main() {
	url := flag.String("url", "http://localhost:8080", "gateway URL")
	key := flag.String("key", os.Getenv("PROOFGATE_KEY"), "API key")
	route := flag.String("route", "default", "route")
	rps := flag.Int("rps", 20, "requests per second")
	before := flag.Duration("before", 20*time.Second, "healthy period")
	after := flag.Duration("after", 60*time.Second, "period after the slowdown")
	mockAdmin := flag.String("mock-admin", "http://localhost:18081", "mockllm-a admin URL")
	slow := flag.Int("slow-ttft-ms", 3000, "TTFT injected into mockllm-a")
	healthy := flag.String("healthy", "mock-b/mock-large", "target that should take over")
	label := flag.String("label", "", "label")
	out := flag.String("out", "", "output JSON")
	flag.Parse()

	client := &http.Client{Timeout: 30 * time.Second}
	var mu sync.Mutex
	var obs []Obs
	start := time.Now()
	total := *before + *after
	tick := time.NewTicker(time.Second / time.Duration(*rps))
	defer tick.Stop()
	switched := false
	var wg sync.WaitGroup
	for now := range tick.C {
		el := now.Sub(start)
		if el >= total {
			break
		}
		if !switched && el >= *before {
			setMode(*mockAdmin, fmt.Sprintf(`{"ttft_ms":%d}`, *slow))
			switched = true
		}
		wg.Add(1)
		go func(at time.Duration) {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{"model": *route, "stream": true, "max_tokens": 5,
				"messages": []map[string]string{{"role": "user", "content": "hi"}}})
			req, _ := http.NewRequest("POST", *url+"/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+*key)
			req.Header.Set("Content-Type", "application/json")
			t0 := time.Now()
			resp, err := client.Do(req)
			o := Obs{At: at}
			if err == nil {
				o.Status, o.Target = resp.StatusCode, resp.Header.Get("X-ProofGate-Target")
				if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err == nil {
					o.TTFT = time.Since(t0)
				}
				resp.Body.Close()
			}
			mu.Lock()
			obs = append(obs, o)
			mu.Unlock()
		}(el)
	}
	wg.Wait()
	setMode(*mockAdmin, `{}`)

	fo, ok := FailoverTime(obs, *before, *healthy, 0.95, time.Second)
	var pre, mid, post []time.Duration
	errors := 0
	for _, o := range obs {
		if o.Status != 200 {
			errors++
			continue
		}
		switch {
		case o.At < *before:
			pre = append(pre, o.TTFT)
		case ok && o.At >= *before+fo:
			post = append(post, o.TTFT)
		default:
			mid = append(mid, o.TTFT)
		}
	}
	res := map[string]any{"label": *label, "route": *route, "rps": *rps, "slow_ttft_ms": *slow,
		"failover_seconds": fo.Seconds(), "converged": ok, "requests": len(obs), "errors": errors,
		"p95_ttft_ms_before": p95(pre), "p95_ttft_ms_after_switch": p95(mid), "p95_ttft_ms_after_failover": p95(post),
		"date": time.Now().UTC().Format(time.RFC3339)}
	b, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(b))
	if *out != "" {
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
