package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Transport handles JSON-RPC over stdin/stdout (one JSON message per line).
type Transport struct {
	reader *bufio.Reader
	writer io.Writer
	server *Server
	mu     sync.Mutex
}

func NewTransport(reader io.Reader, writer io.Writer, server *Server) *Transport {
	return &Transport{
		reader: bufio.NewReader(reader),
		writer: writer,
		server: server,
	}
}

func (t *Transport) Serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			line, err := t.reader.ReadBytes('\n')
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
			payload := strings.TrimSpace(string(line))
			if payload == "" {
				continue
			}
			var req request
			if err := json.Unmarshal([]byte(payload), &req); err != nil {
				_ = t.sendError(nil, ErrorParse, "failed to parse request")
				continue
			}
			_ = t.handleRequest(ctx, &req)
		}
	}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (t *Transport) handleRequest(ctx context.Context, req *request) error {
	switch req.Method {
	case "initialize":
		return t.handleInitialize(req.ID)
	case "tools/list":
		return t.handleListTools(req.ID)
	case "tools/call":
		return t.handleCallTool(ctx, req.ID, req.Params)
	default:
		return t.sendError(req.ID, ErrorMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (t *Transport) handleInitialize(id interface{}) error {
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]bool{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "byte-lsp-mcp",
				"version": "0.1.0",
			},
		},
	}
	return t.sendResponse(resp)
}

func (t *Transport) handleListTools(id interface{}) error {
	tools := toolList()
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]interface{}{
			"tools": tools,
		},
	}
	return t.sendResponse(resp)
}

func (t *Transport) handleCallTool(ctx context.Context, id interface{}, params json.RawMessage) error {
	var req struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return t.sendError(id, ErrorInvalidParams, "invalid tool call params")
	}
	var args map[string]interface{}
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return t.sendError(id, ErrorInvalidParams, "invalid tool arguments")
		}
	}

	result, err := t.server.HandleToolCall(ctx, req.Name, args)
	if err != nil {
		return t.sendError(id, ErrorInternal, err.Error())
	}

	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": formatResult(result),
				},
			},
		},
	}
	return t.sendResponse(resp)
}

func formatResult(result interface{}) string {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Sprintf("error formatting result: %v", err)
	}
	return string(data)
}

func (t *Transport) sendResponse(response interface{}) error {
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return t.writeLine(data)
}

func (t *Transport) sendError(id interface{}, code int, message string) error {
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	}
	return t.sendResponse(resp)
}

func (t *Transport) writeLine(data []byte) error {
	if t.writer == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.writer.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

const (
	ErrorParse          = -32700
	ErrorInvalidRequest = -32600
	ErrorMethodNotFound = -32601
	ErrorInvalidParams  = -32602
	ErrorInternal       = -32603
)
