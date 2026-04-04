package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/iac-studio/iac-studio/internal/ai"
	"github.com/iac-studio/iac-studio/internal/api"
	"github.com/iac-studio/iac-studio/internal/runner"
	"github.com/iac-studio/iac-studio/internal/watcher"
)

const (
	AppName    = "iac-studio"
	AppVersion = "0.1.0"
)

// frontendFS holds the built React frontend. The directory is created by
// `make build-frontend` (or `cd web && npm run build`) before the Go
// build runs. During development (`make dev`), Vite serves the frontend
// directly so this embed is not used.
//
// The Makefile copies web/dist/ into cmd/server/frontend/dist/ before
// building the Go binary so the embed path is relative to this file.
// A bootstrap index.html is committed so `go build` never fails on a
// clean checkout; `make build` replaces it with the real bundle.

//go:embed frontend/dist/*
var frontendFS embed.FS

func main() {
	host := flag.String("host", "127.0.0.1", "Host to bind to")
	port := flag.Int("port", 3000, "Port to listen on")
	projectsDir := flag.String("projects-dir", defaultProjectsDir(), "Directory for IaC projects")
	aiEndpoint := flag.String("ai-endpoint", "http://localhost:11434", "Ollama endpoint")
	aiModel := flag.String("ai-model", "deepseek-coder:6.7b", "AI model name")
	version := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *version {
		fmt.Printf("%s v%s\n", AppName, AppVersion)
		os.Exit(0)
	}

	if err := os.MkdirAll(*projectsDir, 0755); err != nil {
		log.Fatalf("Cannot create projects dir: %v", err)
	}

	printBanner(*host, *port, *projectsDir, *aiEndpoint, *aiModel)

	// Initialize core services
	hub := api.NewHub()
	go hub.Run()

	fw := watcher.New(hub)
	defer fw.Close()

	aiClient := ai.NewOllamaClient(*aiEndpoint, *aiModel)
	run := runner.New()

	// Build router
	router := api.NewRouter(hub, fw, aiClient, run, *projectsDir)

	// Serve embedded frontend
	frontendContent, _ := fs.Sub(frontendFS, "frontend/dist")
	router.Handle("GET /", http.FileServer(http.FS(frontendContent)))

	addr := fmt.Sprintf("%s:%d", *host, *port)
	server := &http.Server{
		Addr:         addr,
		Handler:      api.CORS(router),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("◆ IaC Studio running at http://%s\n", addr)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(shutdownCtx)
}

func defaultProjectsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "iac-projects")
}

func printBanner(host string, port int, projectsDir, aiEndpoint, aiModel string) {
	fmt.Printf("\n  ◆ IaC Studio v%s\n", AppVersion)
	fmt.Println("  ───────────────────────")
	fmt.Println("  Local-first visual IaC builder")
	fmt.Printf("  Projects:  %s\n", projectsDir)
	fmt.Printf("  AI:        %s (%s)\n", aiEndpoint, aiModel)
	fmt.Printf("  Server:    http://%s:%d\n\n", host, port)
}
