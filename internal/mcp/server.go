package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DreamCats/bytelsp/internal/gopls"
	"github.com/DreamCats/bytelsp/internal/tools"
	"github.com/DreamCats/bytelsp/internal/workspace"
)

// Server handles MCP tool calls and bridges to gopls.
type Server struct {
	root        string
	rootURI     string
	client      *gopls.Client
	docs        *gopls.DocumentManager
	diagnostics *diagHub

	initOnce sync.Once
	initErr  error
}

func NewServer(ctx context.Context) (*Server, error) {
	root, err := workspace.DetectRoot(".")
	if err != nil {
		return nil, err
	}
	if err := workspace.ValidateRoot(root); err != nil {
		return nil, err
	}
	rootURI := pathToURI(root)

	return &Server{
		root:        root,
		rootURI:     rootURI,
		diagnostics: newDiagHub(),
	}, nil
}

func (s *Server) Serve(stdin, stdout interface{}) error {
	transport := NewTransport(stdin.(io.Reader), stdout.(io.Writer), s)
	return transport.Serve(context.Background())
}

func (s *Server) Initialize(ctx context.Context) error {
	s.initOnce.Do(func() {
		client, err := gopls.NewClient(&gopls.Config{Workdir: s.root})
		if err != nil {
			s.initErr = err
			return
		}
		s.client = client
		s.docs = gopls.NewDocumentManager(client)

		client.OnNotification("textDocument/publishDiagnostics", func(raw json.RawMessage) {
			diags := parsePublishDiagnostics(raw)
			if diags == nil {
				return
			}
			s.diagnostics.Update(diags.URI, diags.Diagnostics)
		})

		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		s.initErr = client.Initialize(ctx, s.rootURI, []string{s.rootURI})
	})
	return s.initErr
}

func (s *Server) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

func (s *Server) HandleToolCall(ctx context.Context, name string, args map[string]interface{}) (interface{}, error) {
	if err := s.Initialize(ctx); err != nil {
		return nil, err
	}

	switch name {
	case "analyze_code":
		return s.handleAnalyze(ctx, args)
	case "go_to_definition":
		return s.handleDefinition(ctx, args)
	case "find_references":
		return s.handleReferences(ctx, args)
	case "search_symbols":
		return s.handleSearchSymbols(ctx, args)
	case "get_hover":
		return s.handleHover(ctx, args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *Server) handleAnalyze(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var input tools.AnalyzeCodeInput
	if err := decodeArgs(args, &input); err != nil {
		return nil, err
	}
	if input.Code == "" || input.FilePath == "" {
		return nil, errors.New("code and file_path are required")
	}

	absPath, uri, err := s.prepareDocument(ctx, input.FilePath, input.Code)
	if err != nil {
		return nil, err
	}

	pullCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri,
		},
	}
	raw, err := s.client.SendRequest(pullCtx, "textDocument/diagnostic", params)
	var diagnostics []tools.Diagnostic
	if err == nil {
		diagnostics, _ = tools.ParseDiagnostics(raw, uri)
	}
	if len(diagnostics) == 0 {
		if got := s.diagnostics.Wait(uri, 3*time.Second); len(got) > 0 {
			diagnostics = got
		}
	}
	if !input.IncludeWarnings {
		diagnostics = filterErrors(diagnostics)
	}
	if diagnostics == nil {
		diagnostics = []tools.Diagnostic{}
	}

	return &tools.AnalyzeCodeOutput{FilePath: absPath, Diagnostics: diagnostics}, nil
}

func (s *Server) handleDefinition(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var input tools.GoToDefinitionInput
	if err := decodeArgs(args, &input); err != nil {
		return nil, err
	}
	if input.Code == "" || input.FilePath == "" || input.Line < 1 || input.Col < 1 {
		return nil, errors.New("code, file_path, line, col are required")
	}

	_, uri, err := s.prepareDocument(ctx, input.FilePath, input.Code)
	if err != nil {
		return nil, err
	}
	s.warmupDocument(ctx, uri)

	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri,
		},
		"position": map[string]interface{}{
			"line":      input.Line - 1,
			"character": input.Col - 1,
		},
	}
	raw, err := s.client.SendRequest(ctx, "textDocument/definition", params)
	if err != nil && shouldAdjustPosition(err) {
		if nl, nc, ok := adjustPositionFromCode(input.Code, input.Line, input.Col); ok {
			params["position"] = map[string]interface{}{
				"line":      nl - 1,
				"character": nc - 1,
			}
			raw, err = s.client.SendRequest(ctx, "textDocument/definition", params)
		}
	}
	if err != nil {
		return nil, err
	}
	locs, err := tools.ParseLocations(raw)
	if err != nil {
		return nil, err
	}
	return &tools.GoToDefinitionOutput{Locations: locs}, nil
}

func (s *Server) handleReferences(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var input tools.FindReferencesInput
	if err := decodeArgs(args, &input); err != nil {
		return nil, err
	}
	if input.Code == "" || input.FilePath == "" || input.Line < 1 || input.Col < 1 {
		return nil, errors.New("code, file_path, line, col are required")
	}

	_, uri, err := s.prepareDocument(ctx, input.FilePath, input.Code)
	if err != nil {
		return nil, err
	}
	s.warmupDocument(ctx, uri)

	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri,
		},
		"position": map[string]interface{}{
			"line":      input.Line - 1,
			"character": input.Col - 1,
		},
		"context": map[string]interface{}{
			"includeDeclaration": input.IncludeDeclaration,
		},
	}
	raw, err := s.client.SendRequest(ctx, "textDocument/references", params)
	if err != nil && shouldAdjustPosition(err) {
		if nl, nc, ok := adjustPositionFromCode(input.Code, input.Line, input.Col); ok {
			params["position"] = map[string]interface{}{
				"line":      nl - 1,
				"character": nc - 1,
			}
			raw, err = s.client.SendRequest(ctx, "textDocument/references", params)
		}
	}
	if err != nil {
		return nil, err
	}
	locs, err := tools.ParseLocations(raw)
	if err != nil {
		return nil, err
	}

	refs := make([]tools.ReferenceResult, 0, len(locs))
	for _, loc := range locs {
		refs = append(refs, tools.ReferenceResult{Location: loc})
	}
	return &tools.FindReferencesOutput{References: refs}, nil
}

func (s *Server) handleSearchSymbols(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var input tools.SearchSymbolsInput
	if err := decodeArgs(args, &input); err != nil {
		return nil, err
	}
	if input.Query == "" {
		return nil, errors.New("query is required")
	}

	params := map[string]interface{}{
		"query": input.Query,
	}
	raw, err := s.client.SendRequest(ctx, "workspace/symbol", params)
	if err != nil {
		return nil, err
	}
	items, err := tools.ParseSymbols(raw)
	if err != nil {
		return nil, err
	}
	if !input.IncludeExternal {
		items = filterSymbolsInWorkspace(items, s.root)
	}
	return &tools.SearchSymbolsOutput{Symbols: items}, nil
}

func (s *Server) handleHover(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	var input tools.GetHoverInput
	if err := decodeArgs(args, &input); err != nil {
		return nil, err
	}
	if input.Code == "" || input.FilePath == "" || input.Line < 1 || input.Col < 1 {
		return nil, errors.New("code, file_path, line, col are required")
	}

	_, uri, err := s.prepareDocument(ctx, input.FilePath, input.Code)
	if err != nil {
		return nil, err
	}
	s.warmupDocument(ctx, uri)

	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri,
		},
		"position": map[string]interface{}{
			"line":      input.Line - 1,
			"character": input.Col - 1,
		},
	}
	raw, err := s.client.SendRequest(ctx, "textDocument/hover", params)
	if err != nil && shouldAdjustPosition(err) {
		if nl, nc, ok := adjustPositionFromCode(input.Code, input.Line, input.Col); ok {
			params["position"] = map[string]interface{}{
				"line":      nl - 1,
				"character": nc - 1,
			}
			raw, err = s.client.SendRequest(ctx, "textDocument/hover", params)
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := tools.ParseHover(raw)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func decodeArgs(args map[string]interface{}, out interface{}) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func (s *Server) prepareDocument(ctx context.Context, filePath, code string) (string, string, error) {
	absPath, isVirtual, err := s.resolvePath(filePath)
	if err != nil {
		return "", "", err
	}
	if isVirtual {
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return "", "", err
		}
		if err := os.WriteFile(absPath, []byte(code), 0o644); err != nil {
			return "", "", err
		}
	}
	uri := pathToURI(absPath)
	if _, err := s.docs.OpenOrUpdate(ctx, uri, "go", code); err != nil {
		return "", "", err
	}
	return absPath, uri, nil
}

func (s *Server) warmupDocument(ctx context.Context, uri string) {
	pullCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri,
		},
	}
	_, _ = s.client.SendRequest(pullCtx, "textDocument/diagnostic", params)
}

func (s *Server) resolvePath(filePath string) (string, bool, error) {
	cleaned := filepath.Clean(filePath)
	if cleaned == "" || cleaned == "." {
		return "", false, errors.New("file_path cannot be empty")
	}

	virtualBase := filepath.Join(s.root, "mcp_virtual")
	sep := string(os.PathSeparator)

	if filepath.IsAbs(cleaned) {
		if strings.HasPrefix(cleaned, s.root+sep) {
			if _, err := os.Stat(cleaned); err == nil {
				return cleaned, false, nil
			}
			return cleaned, true, nil
		}
		base := filepath.Base(cleaned)
		return filepath.Join(virtualBase, base), true, nil
	}

	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+sep) {
		base := filepath.Base(cleaned)
		return filepath.Join(virtualBase, base), true, nil
	}

	candidate := filepath.Join(s.root, cleaned)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, false, nil
	}
	return filepath.Join(virtualBase, cleaned), true, nil
}

func filterErrors(diags []tools.Diagnostic) []tools.Diagnostic {
	out := make([]tools.Diagnostic, 0, len(diags))
	for _, d := range diags {
		if d.Severity == "error" {
			out = append(out, d)
		}
	}
	return out
}

func filterSymbolsInWorkspace(items []tools.SymbolInformation, root string) []tools.SymbolInformation {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return items
	}
	filtered := make([]tools.SymbolInformation, 0, len(items))
	for _, item := range items {
		if item.FilePath == "" {
			continue
		}
		if inWorkspace(rootAbs, item.FilePath) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func inWorkspace(rootAbs, filePath string) bool {
	fileAbs, err := filepath.Abs(filePath)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, fileAbs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return false
	}
	return true
}

func shouldAdjustPosition(err error) bool {
	msg := err.Error()
	if strings.Contains(msg, "column is beyond end of line") {
		return true
	}
	if strings.Contains(msg, "no identifier found") {
		return true
	}
	if strings.Contains(msg, "invalid position") {
		return true
	}
	return false
}

type identSpan struct {
	start int
	end   int
}

func adjustPositionFromCode(code string, line, col int) (int, int, bool) {
	lines := strings.Split(code, "\n")
	if line < 1 || line > len(lines) {
		return 0, 0, false
	}
	if span, ok := pickSpan(lines[line-1], col); ok {
		return line, span.start, true
	}
	for i := line; i < len(lines); i++ {
		if span, ok := pickSpan(lines[i], 1); ok {
			return i + 1, span.start, true
		}
	}
	for i := line - 2; i >= 0; i-- {
		if span, ok := pickSpan(lines[i], 1); ok {
			return i + 1, span.start, true
		}
	}
	return 0, 0, false
}

func pickSpan(line string, col int) (identSpan, bool) {
	spans := findIdentifierSpans(line)
	if len(spans) == 0 {
		return identSpan{}, false
	}
	if col < 1 {
		col = 1
	}
	for _, sp := range spans {
		if col >= sp.start && col <= sp.end {
			return sp, true
		}
	}
	for _, sp := range spans {
		if sp.start >= col {
			return sp, true
		}
	}
	return spans[len(spans)-1], true
}

func findIdentifierSpans(line string) []identSpan {
	runes := []rune(line)
	spans := make([]identSpan, 0)
	i := 0
	for i < len(runes) {
		if !isIdentStart(runes[i]) {
			i++
			continue
		}
		start := i + 1
		i++
		for i < len(runes) && isIdentPart(runes[i]) {
			i++
		}
		end := i
		spans = append(spans, identSpan{start: start, end: end})
	}
	return spans
}

func isIdentStart(r rune) bool {
	return r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9')
}

type publishDiagnostics struct {
	URI         string             `json:"uri"`
	Diagnostics []tools.Diagnostic `json:"diagnostics"`
}

func parsePublishDiagnostics(raw json.RawMessage) *publishDiagnostics {
	var payload struct {
		URI         string          `json:"uri"`
		Diagnostics json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	if payload.URI == "" {
		return nil
	}
	diags, err := tools.ParseDiagnostics(payload.Diagnostics, payload.URI)
	if err != nil {
		return nil
	}
	return &publishDiagnostics{URI: payload.URI, Diagnostics: diags}
}

func pathToURI(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String()
}

// diagHub stores latest diagnostics and allows waiting for updates.
type diagHub struct {
	mu      sync.Mutex
	latest  map[string][]tools.Diagnostic
	waiters map[string][]chan []tools.Diagnostic
}

func newDiagHub() *diagHub {
	return &diagHub{
		latest:  make(map[string][]tools.Diagnostic),
		waiters: make(map[string][]chan []tools.Diagnostic),
	}
}

func (h *diagHub) Update(uri string, diags []tools.Diagnostic) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.latest[uri] = diags
	for _, ch := range h.waiters[uri] {
		ch <- diags
		close(ch)
	}
	delete(h.waiters, uri)
}

func (h *diagHub) Wait(uri string, timeout time.Duration) []tools.Diagnostic {
	h.mu.Lock()
	if diags, ok := h.latest[uri]; ok {
		h.mu.Unlock()
		return diags
	}
	ch := make(chan []tools.Diagnostic, 1)
	h.waiters[uri] = append(h.waiters[uri], ch)
	h.mu.Unlock()

	select {
	case diags := <-ch:
		return diags
	case <-time.After(timeout):
		return nil
	}
}

func toolList() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "analyze_code",
			"description": "Analyze Go code and return diagnostics.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"code": map[string]interface{}{
						"type":        "string",
						"description": "Go code to analyze",
					},
					"file_path": map[string]interface{}{
						"type":        "string",
						"description": "File path for the code (relative to workspace)",
					},
					"include_warnings": map[string]interface{}{
						"type":        "boolean",
						"description": "Include warnings and hints",
					},
				},
				"required": []string{"code", "file_path"},
			},
		},
		{
			"name":        "go_to_definition",
			"description": "Go to definition for symbol at position.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"code": map[string]interface{}{
						"type":        "string",
						"description": "Go code content",
					},
					"file_path": map[string]interface{}{
						"type":        "string",
						"description": "File path for the code",
					},
					"line": map[string]interface{}{
						"type":        "integer",
						"description": "1-based line number",
					},
					"col": map[string]interface{}{
						"type":        "integer",
						"description": "1-based column number",
					},
				},
				"required": []string{"code", "file_path", "line", "col"},
			},
		},
		{
			"name":        "find_references",
			"description": "Find references for symbol at position.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"code": map[string]interface{}{
						"type":        "string",
						"description": "Go code content",
					},
					"file_path": map[string]interface{}{
						"type":        "string",
						"description": "File path for the code",
					},
					"line": map[string]interface{}{
						"type":        "integer",
						"description": "1-based line number",
					},
					"col": map[string]interface{}{
						"type":        "integer",
						"description": "1-based column number",
					},
					"include_declaration": map[string]interface{}{
						"type":        "boolean",
						"description": "Include declaration in results",
					},
				},
				"required": []string{"code", "file_path", "line", "col"},
			},
		},
		{
			"name":        "search_symbols",
			"description": "Search symbols in workspace by name.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Symbol query",
					},
					"include_external": map[string]interface{}{
						"type":        "boolean",
						"description": "Include symbols outside the workspace (stdlib, module cache)",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "get_hover",
			"description": "Get hover information at position.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"code": map[string]interface{}{
						"type":        "string",
						"description": "Go code content",
					},
					"file_path": map[string]interface{}{
						"type":        "string",
						"description": "File path for the code",
					},
					"line": map[string]interface{}{
						"type":        "integer",
						"description": "1-based line number",
					},
					"col": map[string]interface{}{
						"type":        "integer",
						"description": "1-based column number",
					},
				},
				"required": []string{"code", "file_path", "line", "col"},
			},
		},
	}
}
