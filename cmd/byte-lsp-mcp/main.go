package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/DreamCats/bytelsp/internal/mcp"
)

const version = "0.1.0"

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(0)

	showVersion := flag.Bool("version", false, "print version and exit")
	showHelp := flag.Bool("h", false, "print help and exit")
	flag.BoolVar(showHelp, "help", false, "print help and exit")
	flag.Parse()

	if *showHelp {
		printHelp()
		return
	}
	if *showVersion {
		fmt.Println(version)
		return
	}

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

func printHelp() {
	fmt.Print(`byte-lsp-mcp (MCP server)

Usage:
  byte-lsp-mcp [flags]

Flags:
  -h, -help     show help
  -version      show version

MCP config example (Claude Code / Desktop):
{
  "mcpServers": {
    "byte-lsp": {
      "command": "/path/to/byte-lsp-mcp",
      "args": []
    }
  }
}
`)
}
