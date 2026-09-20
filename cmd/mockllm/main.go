// Command mockllm runs a controllable OpenAI-compatible provider.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/proofgate/proofgate/internal/mockllm"
)

func main() {
	addr := flag.String("addr", ":8081", "listen address")
	name := flag.String("name", "mock", "instance name used in response ids")
	ttft := flag.Int("ttft-ms", 0, "time to first token in ms")
	tps := flag.Int("tps", 0, "tokens per second (0 = unlimited)")
	flag.Parse()
	s := mockllm.New(*name, mockllm.Mode{TTFTMs: *ttft, TokensPerSec: *tps})
	srv := &http.Server{Addr: *addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("mockllm %s listening on %s", *name, *addr)
	log.Fatal(srv.ListenAndServe())
}
