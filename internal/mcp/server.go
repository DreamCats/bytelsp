package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dreamcats/bytelsp/internal/gopls"
	"github.com/dreamcats/bytelsp/internal/tools"
	"github.com/dreamcats/bytelsp/internal/workspace"
)

// Service implements gopls-backed MCP tools.
type Service struct {
	root        string
	rootURI     string
	client      *gopls.Client
	docs        *gopls.DocumentManager
	diagnostics *diagHub

	initOnce sync.Once
	initErr  error
}

func NewService(ctx context.Context) (*Service, error) {
	root, err := workspace.DetectRoot(".")
	if err != nil {
		return nil, err
	}
	if err := workspace.ValidateRoot(root); err != nil {
		return nil, err
	}
	rootURI := pathToURI(root)

	return &Service{
		root:        root,
		rootURI:     rootURI,
		diagnostics: newDiagHub(),
	}, nil
}

func (s *Service) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

func (s *Service) Register(server *sdk.Server) {
	// Primary tool: search for symbols by name
	sdk.AddTool(server, &sdk.Tool{
		Name: "search_symbols",
		Description: `Search for Go symbols (functions, types, variables, methods) by name pattern.

START HERE when you need to find code. This is your entry point for exploring a Go codebase.

Examples:
- "Handler" → finds RequestHandler, ErrorHandler, etc.
- "New" → finds NewClient, NewService, etc.
- "Parse" → finds ParseConfig, ParseJSON, etc.

Usage: Just provide a query string. Results include symbol name, kind, file path, and line number.
Then use explain_symbol to understand any symbol you find.`,
	}, s.SearchSymbols)

	// Primary tool: understand a symbol completely
	sdk.AddTool(server, &sdk.Tool{
		Name: "explain_symbol",
		Description: `Get complete information about a Go symbol in one call.

USE THIS to understand any function, type, method, or variable. Returns everything you need:
- Signature and type information
- Documentation comments
- Source code
- Definition location
- Where it's used (references)

This replaces the need for separate definition/hover/references calls.

Usage: file_path + symbol name. File content is read from disk automatically.`,
	}, s.ExplainSymbol)

	// Primary tool: understand call flow
	sdk.AddTool(server, &sdk.Tool{
		Name: "get_call_hierarchy",
		Description: `Analyze who calls a function and what it calls.

USE THIS to understand code flow:
- "incoming": who calls this function? (callers)
- "outgoing": what does this function call? (callees)
- "both": show both directions (default)

Essential for: tracing request flow, understanding dependencies, assessing refactoring impact.

Usage: file_path + symbol name. Direction defaults to "both".`,
	}, s.GetCallHierarchy)

	// Tool for exploring external dependencies (go/pkg/mod)
	sdk.AddTool(server, &sdk.Tool{
		Name: "explain_import",
		Description: `Get type/function information from an imported package (including external dependencies).

USE THIS when you need to understand types from:
- Third-party libraries (e.g., protobuf/thrift generated code)
- Company internal packages (e.g., RPC request/response types)
- Standard library types

This tool directly parses the source code without requiring gopls indexing,
making it fast even for large generated files (like thrift IDL).

Examples:
- import_path: "encoding/json", symbol: "Decoder"
- import_path: "github.com/xxx/idl/user", symbol: "GetUserInfoRequest"

Returns: Type definition, fields (for structs), methods, documentation.`,
	}, s.ExplainImport)

	server.AddResource(&sdk.Resource{
		URI:         "byte-lsp://about",
		Name:        "byte-lsp-mcp",
		Title:       "Byte LSP MCP Server",
		Description: "Go language analysis tools: search_symbols, explain_symbol, explain_import, get_call_hierarchy.",
		MIMEType:    "text/plain",
	}, s.readAbout)
}

func (s *Service) Initialize(ctx context.Context) error {
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

func (s *Service) SearchSymbols(ctx context.Context, _ *sdk.CallToolRequest, input tools.SearchSymbolsInput) (*sdk.CallToolResult, tools.SearchSymbolsOutput, error) {
	if input.Query == "" {
		return nil, tools.SearchSymbolsOutput{}, errors.New("query is required")
	}
	if err := s.Initialize(ctx); err != nil {
		return nil, tools.SearchSymbolsOutput{}, err
	}

	params := map[string]interface{}{
		"query": input.Query,
	}
	raw, err := s.client.SendRequest(ctx, "workspace/symbol", params)
	if err != nil {
		return nil, tools.SearchSymbolsOutput{}, err
	}
	items, err := tools.ParseSymbols(raw)
	if err != nil {
		return nil, tools.SearchSymbolsOutput{}, err
	}
	if !input.IncludeExternal {
		items = filterSymbolsInWorkspace(items, s.root)
	}
	return nil, tools.SearchSymbolsOutput{Symbols: items}, nil
}

func (s *Service) ExplainSymbol(ctx context.Context, _ *sdk.CallToolRequest, input tools.ExplainSymbolInput) (*sdk.CallToolResult, tools.ExplainSymbolOutput, error) {
	if input.FilePath == "" || input.Symbol == "" {
		return nil, tools.ExplainSymbolOutput{}, errors.New("file_path and symbol are required")
	}
	if err := s.Initialize(ctx); err != nil {
		return nil, tools.ExplainSymbolOutput{}, err
	}

	// Set defaults
	includeSource := true
	if !input.IncludeSource {
		// Check if explicitly set to false (Go zero value issue)
		// We treat zero value as true for better UX
	}
	includeRefs := true
	if !input.IncludeReferences {
		// Same as above
	}
	maxRefs := input.MaxReferences
	if maxRefs <= 0 {
		maxRefs = 10
	}

	// Read file from disk
	absPath, err := s.resolveDiskPath(input.FilePath)
	if err != nil {
		return nil, tools.ExplainSymbolOutput{}, err
	}
	code, err := os.ReadFile(absPath)
	if err != nil {
		return nil, tools.ExplainSymbolOutput{}, err
	}

	// Find symbol position
	line, col, ok := tools.FindSymbolPosition(string(code), input.Symbol)
	if !ok {
		return nil, tools.ExplainSymbolOutput{}, errors.New("symbol not found in file")
	}

	// Prepare document
	_, uri, err := s.prepareDocument(ctx, absPath, string(code))
	if err != nil {
		return nil, tools.ExplainSymbolOutput{}, err
	}
	s.warmupDocument(ctx, uri)

	output := tools.ExplainSymbolOutput{
		Name: input.Symbol,
	}

	// 1. Get hover info (signature + documentation)
	hoverParams := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line - 1, "character": col - 1},
	}
	if hoverRaw, err := s.client.SendRequest(ctx, "textDocument/hover", hoverParams); err == nil {
		if hover, err := tools.ParseHover(hoverRaw); err == nil {
			output.Signature, output.Doc = parseHoverContents(hover.Contents)
		}
	}

	// 2. Get definition location
	defParams := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line - 1, "character": col - 1},
	}
	if defRaw, err := s.client.SendRequest(ctx, "textDocument/definition", defParams); err == nil {
		if locs, err := tools.ParseLocations(defRaw); err == nil && len(locs) > 0 {
			output.DefinedAt = &locs[0]
			output.Kind = inferSymbolKind(string(code), input.Symbol)

			// 3. Extract source code if requested
			if includeSource {
				output.Source = extractSymbolSource(locs[0].FilePath, locs[0].Line)
			}
		}
	}

	// 4. Find references if requested
	if includeRefs {
		refParams := map[string]any{
			"textDocument": map[string]any{"uri": uri},
			"position":     map[string]any{"line": line - 1, "character": col - 1},
			"context":      map[string]any{"includeDeclaration": false},
		}
		if refRaw, err := s.client.SendRequest(ctx, "textDocument/references", refParams); err == nil {
			if locs, err := tools.ParseLocations(refRaw); err == nil {
				output.ReferencesCount = len(locs)
				// Return up to maxRefs references with context
				for i, loc := range locs {
					if i >= maxRefs {
						break
					}
					ref := tools.ReferenceContext{
						FilePath: loc.FilePath,
						Line:     loc.Line,
						Col:      loc.Col,
						Context:  getLineContent(loc.FilePath, loc.Line),
					}
					output.References = append(output.References, ref)
				}
			}
		}
	}

	return nil, output, nil
}

func (s *Service) GetCallHierarchy(ctx context.Context, _ *sdk.CallToolRequest, input tools.GetCallHierarchyInput) (*sdk.CallToolResult, tools.GetCallHierarchyOutput, error) {
	if input.FilePath == "" || input.Symbol == "" {
		return nil, tools.GetCallHierarchyOutput{}, errors.New("file_path and symbol are required")
	}
	if err := s.Initialize(ctx); err != nil {
		return nil, tools.GetCallHierarchyOutput{}, err
	}

	// Set defaults
	direction := input.Direction
	if direction == "" {
		direction = "both"
	}
	if direction != "incoming" && direction != "outgoing" && direction != "both" {
		return nil, tools.GetCallHierarchyOutput{}, errors.New("direction must be 'incoming', 'outgoing', or 'both'")
	}

	// Read file from disk
	absPath, err := s.resolveDiskPath(input.FilePath)
	if err != nil {
		return nil, tools.GetCallHierarchyOutput{}, err
	}
	code, err := os.ReadFile(absPath)
	if err != nil {
		return nil, tools.GetCallHierarchyOutput{}, err
	}

	// Find symbol position
	line, col, ok := tools.FindSymbolPosition(string(code), input.Symbol)
	if !ok {
		return nil, tools.GetCallHierarchyOutput{}, errors.New("symbol not found in file")
	}

	// Prepare document
	_, uri, err := s.prepareDocument(ctx, absPath, string(code))
	if err != nil {
		return nil, tools.GetCallHierarchyOutput{}, err
	}
	s.warmupDocument(ctx, uri)

	// Step 1: Prepare call hierarchy
	prepareParams := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line - 1, "character": col - 1},
	}
	prepareRaw, err := s.client.SendRequest(ctx, "textDocument/prepareCallHierarchy", prepareParams)
	if err != nil {
		return nil, tools.GetCallHierarchyOutput{}, err
	}

	items, err := tools.ParseCallHierarchyPrepare(prepareRaw)
	if err != nil {
		return nil, tools.GetCallHierarchyOutput{}, err
	}
	if len(items) == 0 {
		return nil, tools.GetCallHierarchyOutput{}, errors.New("no call hierarchy item found for symbol")
	}

	item := items[0]
	output := tools.GetCallHierarchyOutput{
		Name:     item.Name,
		Kind:     tools.SymbolKindToString(item.Kind),
		FilePath: tools.URIToPath(item.URI),
		Line:     item.Line(),
	}

	// Step 2: Get incoming calls (callers)
	if direction == "incoming" || direction == "both" {
		incomingParams := map[string]any{
			"item": tools.ConvertToLSPCallHierarchyItem(item),
		}
		if incomingRaw, err := s.client.SendRequest(ctx, "callHierarchy/incomingCalls", incomingParams); err == nil {
			if incoming, err := tools.ParseCallHierarchyIncoming(incomingRaw); err == nil {
				// Add context for each caller
				for i := range incoming {
					incoming[i].Context = getLineContent(incoming[i].FilePath, incoming[i].Line)
				}
				output.Incoming = incoming
			}
		}
	}

	// Step 3: Get outgoing calls (callees)
	if direction == "outgoing" || direction == "both" {
		outgoingParams := map[string]any{
			"item": tools.ConvertToLSPCallHierarchyItem(item),
		}
		if outgoingRaw, err := s.client.SendRequest(ctx, "callHierarchy/outgoingCalls", outgoingParams); err == nil {
			if outgoing, err := tools.ParseCallHierarchyOutgoing(outgoingRaw); err == nil {
				// Add context for each callee
				for i := range outgoing {
					outgoing[i].Context = getLineContent(outgoing[i].FilePath, outgoing[i].Line)
				}
				output.Outgoing = outgoing
			}
		}
	}

	return nil, output, nil
}

func (s *Service) ExplainImport(ctx context.Context, _ *sdk.CallToolRequest, input tools.ExplainImportInput) (*sdk.CallToolResult, tools.ExplainImportOutput, error) {
	if input.ImportPath == "" || input.Symbol == "" {
		return nil, tools.ExplainImportOutput{}, errors.New("import_path and symbol are required")
	}

	// Resolve import path to directory
	pkg, err := tools.ResolveImportPath(s.root, input.ImportPath)
	if err != nil {
		return nil, tools.ExplainImportOutput{}, fmt.Errorf("failed to resolve import path: %w", err)
	}

	if pkg.Dir == "" {
		return nil, tools.ExplainImportOutput{}, fmt.Errorf("package %s has no source directory", input.ImportPath)
	}

	// Parse the symbol from the package
	result, err := tools.ParseSymbolFromPackage(pkg.Dir, pkg.GoFiles, input.Symbol)
	if err != nil {
		return nil, tools.ExplainImportOutput{}, err
	}

	result.ImportPath = input.ImportPath
	return nil, *result, nil
}

// parseHoverContents splits hover contents into signature and documentation.
func parseHoverContents(contents string) (signature, doc string) {
	// Hover typically contains markdown with code block for signature
	// and plain text for documentation
	lines := strings.Split(contents, "\n")
	inCodeBlock := false
	var sigLines, docLines []string

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			sigLines = append(sigLines, line)
		} else if strings.TrimSpace(line) != "" {
			docLines = append(docLines, line)
		}
	}

	return strings.Join(sigLines, "\n"), strings.Join(docLines, "\n")
}

// inferSymbolKind guesses the symbol kind from code context.
func inferSymbolKind(code, symbol string) string {
	lines := strings.Split(code, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Check for function/method
		if strings.HasPrefix(trimmed, "func ") && strings.Contains(line, symbol) {
			if strings.Contains(trimmed, ") "+symbol) || strings.Contains(trimmed, "func (") {
				return "Method"
			}
			return "Function"
		}
		// Check for type
		if strings.HasPrefix(trimmed, "type "+symbol) {
			if strings.Contains(line, "struct") {
				return "Struct"
			}
			if strings.Contains(line, "interface") {
				return "Interface"
			}
			return "Type"
		}
		// Check for const
		if strings.HasPrefix(trimmed, "const ") && strings.Contains(line, symbol) {
			return "Constant"
		}
		// Check for var
		if strings.HasPrefix(trimmed, "var ") && strings.Contains(line, symbol) {
			return "Variable"
		}
	}
	return "Unknown"
}

// extractSymbolSource extracts the source code of a symbol definition.
func extractSymbolSource(filePath string, startLine int) string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if startLine < 1 || startLine > len(lines) {
		return ""
	}

	// Find the end of the symbol (matching braces or end of declaration)
	var result []string
	braceCount := 0
	started := false

	for i := startLine - 1; i < len(lines) && i < startLine+100; i++ {
		line := lines[i]
		result = append(result, line)

		braceCount += strings.Count(line, "{") - strings.Count(line, "}")

		if strings.Contains(line, "{") {
			started = true
		}

		// End conditions
		if started && braceCount == 0 {
			break
		}
		// Single line declaration (no braces)
		if !started && i > startLine-1 && !strings.HasSuffix(strings.TrimSpace(line), ",") {
			break
		}
	}

	// Limit output size
	source := strings.Join(result, "\n")
	if len(source) > 2000 {
		source = source[:2000] + "\n// ... (truncated)"
	}
	return source
}

// getLineContent reads a specific line from a file.
func getLineContent(filePath string, lineNum int) string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if lineNum < 1 || lineNum > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[lineNum-1])
}

func (s *Service) readAbout(ctx context.Context, _ *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
	content := "byte-lsp-mcp provides gopls-backed Go analysis tools: diagnostics, definition, references, hover, and symbol search."
	return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{
		{
			URI:      "byte-lsp://about",
			MIMEType: "text/plain",
			Text:     content,
		},
	}}, nil
}

func (s *Service) prepareDocument(ctx context.Context, filePath, code string) (string, string, error) {
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

func (s *Service) resolvePath(filePath string) (string, bool, error) {
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

func (s *Service) resolveDiskPath(filePath string) (string, error) {
	cleaned := filepath.Clean(filePath)
	if cleaned == "" || cleaned == "." {
		return "", errors.New("file_path cannot be empty")
	}

	if filepath.IsAbs(cleaned) {
		if _, err := os.Stat(cleaned); err == nil {
			return cleaned, nil
		} else {
			return "", err
		}
	}

	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return "", errors.New("file_path escapes workspace; use absolute path")
	}

	candidate := filepath.Join(s.root, cleaned)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	} else {
		return "", err
	}
}

func (s *Service) warmupDocument(ctx context.Context, uri string) {
	pullCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri,
		},
	}
	_, _ = s.client.SendRequest(pullCtx, "textDocument/diagnostic", params)
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

type publishDiagnostics struct {
	URI         string             `json:"uri"`
	Diagnostics []tools.Diagnostic `json:"diagnostics"`
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

