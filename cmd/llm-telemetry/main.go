package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stratecode/lab/internal/llmtelemetry"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8081", "Loopback bind address")
	upstream := flag.String("upstream", "http://127.0.0.1:8080", "llama.cpp loopback upstream")
	model := flag.String("model", "Qwen3.5-4B-Q4_K_M", "Fixed model label; never derived from user input")
	flag.Parse()
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("listen must be a loopback IP")
	}
	target, err := url.Parse(*upstream)
	if err != nil || target.User != nil || target.Scheme != "http" || net.ParseIP(target.Hostname()) == nil || !net.ParseIP(target.Hostname()).IsLoopback() || (target.Path != "" && target.Path != "/") || target.RawQuery != "" {
		log.Fatal("upstream must be a loopback HTTP origin without credentials")
	}
	server := &http.Server{Addr: *listen, Handler: llmtelemetry.New(target, *model, prometheus.NewRegistry()), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("LLM telemetry listening on %s", *listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
