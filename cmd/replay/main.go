// Command replay sends a two-phase dataset through ProofGate and reports cache hit and wrong-hit rates.
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
	"sync"
	"time"
)

type line struct {
	ID     string `json:"id"`
	Group  string `json:"group"`
	Phase  int    `json:"phase"`
	Prompt string `json:"prompt"`
}

func main() {
	url := flag.String("url", "http://localhost:8080", "gateway base URL")
	key := flag.String("key", os.Getenv("PROOFGATE_KEY"), "API key")
	file := flag.String("file", "bench/datasets/qqp-replay.jsonl", "replay file")
	route := flag.String("route", "faq", "route to use as model")
	conc := flag.Int("concurrency", 8, "parallel requests")
	settle := flag.Duration("settle", 2*time.Second, "pause between phases (background cache writes)")
	label := flag.String("label", "", "free-text label stored in the report")
	out := flag.String("out", "", "write the JSON report here")
	flag.Parse()

	f, err := os.Open(*file)
	if err != nil {
		log.Fatal(err)
	}
	var phases [3][]line
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			log.Fatal(err)
		}
		if l.Phase >= 1 && l.Phase <= 2 {
			phases[l.Phase] = append(phases[l.Phase], l)
		}
	}
	f.Close()

	client := &http.Client{Timeout: 60 * time.Second}
	var mu sync.Mutex
	var results []Result
	groups := map[string]string{}
	run := func(ls []line) {
		sem := make(chan struct{}, *conc)
		var wg sync.WaitGroup
		for _, l := range ls {
			wg.Add(1)
			sem <- struct{}{}
			go func(l line) {
				defer func() { <-sem; wg.Done() }()
				body, _ := json.Marshal(map[string]any{
					"model":      *route,
					"max_tokens": 64,
					"messages":   []map[string]string{{"role": "user", "content": l.Prompt}},
				})
				req, _ := http.NewRequest("POST", *url+"/v1/chat/completions", bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+*key)
				req.Header.Set("Content-Type", "application/json")
				start := time.Now()
				resp, err := client.Do(req)
				res := Result{Phase: l.Phase, Group: l.Group, Latency: time.Since(start)}
				if err == nil {
					res.Status = resp.StatusCode
					res.ReqID = resp.Header.Get("X-ProofGate-Request-Id")
					res.Cache = resp.Header.Get("X-ProofGate-Cache")
					res.Source = resp.Header.Get("X-ProofGate-Cache-Source")
					resp.Body.Close()
				}
				mu.Lock()
				results = append(results, res)
				if res.ReqID != "" {
					groups[res.ReqID] = l.Group
				}
				mu.Unlock()
			}(l)
		}
		wg.Wait()
	}
	run(phases[1])
	time.Sleep(*settle)
	run(phases[2])

	rep := Summarize(results, groups)
	rep.Label, rep.Dataset, rep.Route, rep.Date = *label, *file, *route, time.Now().UTC().Format(time.RFC3339)
	b, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(b))
	if *out != "" {
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
