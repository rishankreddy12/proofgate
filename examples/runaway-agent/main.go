// Command runaway-agent simulates an agent stuck calling the same tool, and one that keeps working until
// its run budget ends. It shows where ProofGate stops each run.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

type result struct {
	Scenario        string `json:"scenario"`
	StepsBeforeStop int    `json:"steps_before_block"`
	Status          int    `json:"status"`
	Code            string `json:"code"`
	Message         string `json:"message"`
}

func call(url, key, run string, msgs []map[string]any) (int, map[string]any) {
	body, _ := json.Marshal(map[string]any{"model": "default", "max_tokens": 16, "messages": msgs})
	req, _ := http.NewRequest("POST", url+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ProofGate-Run-Id", run)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func runScenario(name, url, key, run string, varyArgs bool) result {
	msgs := []map[string]any{{"role": "user", "content": "What is the weather in Pune?"}}
	for step := 1; step <= 50; step++ {
		status, out := call(url, key, run, msgs)
		if status != 200 {
			e, _ := out["error"].(map[string]any)
			return result{Scenario: name, StepsBeforeStop: step - 1, Status: status, Code: fmt.Sprint(e["code"]), Message: fmt.Sprint(e["message"])}
		}
		q := "weather pune"
		if varyArgs {
			q = fmt.Sprintf("weather pune attempt %c", 'a'+rune(step%26)) // letters, not digits: not normalised away
		}
		id := fmt.Sprintf("call_%d", step)
		msgs = append(msgs,
			map[string]any{"role": "assistant", "tool_calls": []map[string]any{{"id": id, "type": "function",
				"function": map[string]any{"name": "search", "arguments": fmt.Sprintf(`{"q":%q}`, q)}}}},
			map[string]any{"role": "tool", "tool_call_id": id, "content": fmt.Sprintf("No results (took %d ms)", 10+step)})
	}
	return result{Scenario: name, StepsBeforeStop: 50, Status: 200, Code: "not_stopped"}
}

func main() {
	url := flag.String("url", "http://localhost:8080", "gateway URL")
	loopKey := flag.String("loop-key", os.Getenv("LOOP_KEY"), "key with a run policy (loop detection)")
	budgetKey := flag.String("budget-key", os.Getenv("BUDGET_KEY"), "key with a small max_cost_usd")
	out := flag.String("out", "bench/results/phase4/runaway-agent.json", "output")
	flag.Parse()
	res := []result{
		runScenario("identical tool call every step", *url, *loopKey, "demo-loop", false),
		runScenario("new arguments every step until the budget ends", *url, *budgetKey, "demo-budget", true),
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(b))
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
}
