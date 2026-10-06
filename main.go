package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	loadDotEnv(".env")

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	// Subcommand: ./mailbridge test [recipient@email.com]
	// Sends one test email through every configured provider and exits.
	if len(os.Args) > 1 && os.Args[1] == "test" {
		runTestProviders(cfg)
		return
	}

	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("store error: %v", err)
	}
	defer store.Close()

	handler := NewHandler(
		cfg,
		store,
		NewSendPulseSender(cfg.SendPulseClientID, cfg.SendPulseClientSecret),
		NewSMTP2GOSender(cfg.SMTP2GOAPIKey),
		NewPostmarkSender(cfg.PostmarkToken),
		NewMailtrapSender(cfg.MailtrapToken),
		NewMailerooSender(cfg.MailerooAPIKey),
		NewMailgunSender(cfg.MailgunAPIKey, cfg.MailgunDomain),
	)

	mux := http.NewServeMux()
	mux.Handle("POST /webhook", handler)
	mux.Handle("POST /inbound", handler) // alias — works regardless of which path ForwardEmail calls
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("mailbridge listening on :%s", cfg.Port)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		if key != "" && os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}
