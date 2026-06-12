package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/iac-studio/iac-studio/internal/mcp"
)

const AppVersion = "0.1.0"

func main() {
	projectsDir := flag.String("projects-dir", defaultProjectsDir(), "Directory for IaC Studio projects")
	approvalToken := flag.String("approval-token", os.Getenv("IAC_STUDIO_MCP_APPROVAL_TOKEN"), "Approval token for MCP actions that create local review artifacts or request high-risk workflows")
	version := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *version {
		fmt.Printf("%s v%s\n", mcp.ServerName, AppVersion)
		return
	}
	if err := os.MkdirAll(*projectsDir, 0o755); err != nil {
		log.Fatalf("create projects dir: %v", err)
	}

	log.SetOutput(os.Stderr)
	log.Printf("%s starting with projects dir %s", mcp.ServerName, *projectsDir)
	if *approvalToken == "" {
		log.Printf("approval token is not configured; mutating MCP tools will return approval_required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server := mcp.NewServer(mcp.Config{
		ProjectsDir:   *projectsDir,
		ApprovalToken: *approvalToken,
		Version:       AppVersion,
	})
	if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil && ctx.Err() == nil {
		log.Fatalf("mcp server error: %v", err)
	}
}

func defaultProjectsDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return "iac-projects"
	}
	return filepath.Join(home, "iac-projects")
}
