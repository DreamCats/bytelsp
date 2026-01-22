package main

import (
	"context"
	"log"
	"os"

	"github.com/bytedance/byte-lsp-mcp/internal/mcp"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(0)

	ctx := context.Background()
	server, err := mcp.NewServer(ctx)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}
	defer server.Close()

	if err := server.Serve(os.Stdin, os.Stdout); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
