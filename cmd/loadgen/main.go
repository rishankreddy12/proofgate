// Command loadgen sends open-loop load at a gateway (or straight at the mock provider) and records
// time to first token and total latency.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type kvFlag map[string]any

func (k kvFlag) String() string { return "" }
func (k kvFlag) Set(v string) error {
	a, b, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("meta must be key=value")
	}
	k[a] = b
	return nil
}

func main() {
	url := flag.String("url", "http://localhost:8080", "base URL (gateway or mock)")
	key := flag.String("key", os.Getenv("PROOFGATE_KEY"), "API key; empty for a direct-to-mock baseline")
	route := flag.String("route", "default", "model/route name")
	rps := flag.Float64("rps", 200, "requests per second (open loop)")
	dur := flag.Duration("duration", 30*time.Second, "measurement window")
	warmup := flag.Duration("warmup", 10*time.Second, "warm-up, excluded from results")
	stream := flag.Bool("stream", false, "use streaming")
	maxTokens := flag.Int("max-tokens", 64, "max tokens")
	promptChars := flag.Int("prompt-chars", 200, "prompt size")
	label := flag.String("label", "", "label")
	out := flag.String("out", "", "output JSON")
	meta := kvFlag{}
	flag.Var(meta, "meta", "extra metadata key=value (repeatable)")
	flag.Parse()

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        20000,
			MaxIdleConnsPerHost: 20000,
			IdleConnTimeout:     90 * time.Second,
			ForceAttemptHTTP2:   true,
		},
		Timeout: 120 * time.Second,
	}
	filler := strings.Repeat("benchmark ", 1+*promptChars/10)[:*promptChars]
	var counter atomic.Int64
	var mu sync.Mutex
	var recs []Record
	var inflight atomic.Int32
	start := time.Now()

	send := func(at time.Duration, record bool) {
		defer inflight.Add(-1)
		n := counter.Add(1)
		body, _ := json.Marshal(map[string]any{
			"model":      *route,
			"stream":     *stream,
			"max_tokens": *maxTokens,
			"messages":   []map[string]string{{"role": "user", "content": fmt.Sprintf("%s #%d", filler, n)}},
		})
		req, _ := http.NewRequest("POST", *url+"/v1/chat/completions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if *key != "" {
			req.Header.Set("Authorization", "Bearer "+*key)
		}
		t0 := time.Now()
		r := Record{Start: at}
		resp, err := client.Do(req)
		if err == nil {
			r.Status = resp.StatusCode
			r.Target = resp.Header.Get("X-ProofGate-Target")
			if *stream {
				br := bufio.NewReader(resp.Body)
				if _, err := br.ReadString('\n'); err == nil {
					r.TTFT = time.Since(t0)
				}
				_, _ = io.Copy(io.Discard, br)
			} else {
				_, _ = io.Copy(io.Discard, resp.Body)
			}
			resp.Body.Close()
		}
		r.Total = time.Since(t0)
		if record {
			mu.Lock()
			recs = append(recs, r)
			mu.Unlock()
		}
	}

	tick := time.NewTicker(time.Duration(float64(time.Second) / *rps))
	defer tick.Stop()
	total := *warmup + *dur
	for now := range tick.C {
		el := now.Sub(start)
		if el >= total {
			break
		}
		if inflight.Load() > 10000 { // overload guard
			mu.Lock()
			recs = append(recs, Record{Start: el})
			mu.Unlock()
			continue
		}
		inflight.Add(1)
		go send(el, el >= *warmup)
	}
	for inflight.Load() > 0 {
		time.Sleep(20 * time.Millisecond)
	}

	if _, ok := meta["commit"]; !ok {
		if b, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output(); err == nil {
			meta["commit"] = strings.TrimSpace(string(b))
		}
	}
	meta["date"] = time.Now().UTC().Format(time.RFC3339)
	meta["host_cpus"] = runtime.NumCPU()
	meta["stream"] = *stream
	meta["max_tokens"] = *maxTokens
	meta["prompt_chars"] = *promptChars
	s := Summarize(recs, *label, *url, *route, *rps, *dur, meta)
	b, _ := json.MarshalIndent(s, "", "  ")
	fmt.Println(string(b))
	if *out != "" {
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
