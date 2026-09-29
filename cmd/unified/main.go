package main

import (
	"errors"
	"fntv-proxy/internal/unified"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	path := os.Getenv("CONFIG")
	if path == "" {
		path = "/app/configs/unified.yaml"
	}
	cfg, err := unified.Load(path)
	if err != nil {
		log.Fatal(err)
	}
	server, err := unified.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-signals; _ = server.Stop() }()
	log.Printf("unified %s listening on %s with %d services", cfg.Role, cfg.Listen, len(cfg.Services))
	err = server.Start()
	_ = server.Stop()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
