package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dreamcats/bytelsp/internal/mcp"
)

const version = "1.0.0"

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
	service, err := mcp.NewService(ctx)
	if err != nil {
		log.Fatalf("failed to create service: %v", err)
	}
	defer service.Close()

	server := sdk.NewServer(&sdk.Implementation{
		Name:       "byte-lsp-mcp",
		Title:      "Byte LSP MCP (gopls-based Go analysis)",
		Version:    version,
		WebsiteURL: "https://github.com/dreamcats/bytelsp",
	}, nil)
	service.Register(server)

	if err := server.Run(ctx, &sdk.StdioTransport{}); err != nil {
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
