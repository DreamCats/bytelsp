package mcp

import (
	"context"
	"encoding/json"
	"errors"
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
	sdk.AddTool(server, &sdk.Tool{
		Name: "analyze_code",
		Description: `Analyze Go code for compilation errors and diagnostics.

Use this to:
- Check if code compiles correctly
- Find syntax errors, type mismatches, undefined references
- Get warnings about unused variables, shadowed imports, etc.

Returns: List of diagnostics with location, severity, and message.`,
	}, s.AnalyzeCode)

	sdk.AddTool(server, &sdk.Tool{
		Name: "go_to_definition",
		Description: `Navigate to where a symbol (function, type, variable, method) is defined.

Use this to:
- Find the implementation of a function
- Locate type/struct definitions
- Trace where a variable is declared

Recommended usage: file_path + symbol + use_disk=true
Alternative: file_path + line/col + code

Returns: Definition location(s) with file path and position.`,
	}, s.GoToDefinition)

	sdk.AddTool(server, &sdk.Tool{
		Name: "find_references",
		Description: `Find all references to a symbol across the codebase.

Use this to:
- See where a function is called
- Find all usages of a type or variable
- Understand impact before refactoring

Recommended usage: file_path + symbol + use_disk=true
Alternative: file_path + line/col + code

Returns: List of reference locations with file paths and positions.`,
	}, s.FindReferences)

	sdk.AddTool(server, &sdk.Tool{
		Name: "search_symbols",
		Description: `Search for symbols (functions, types, variables) by name pattern.

Use this to:
- Find a function when you know part of its name
- Discover available types matching a pattern
- Explore the codebase structure

Supports partial matching (e.g., "Handler" finds "RequestHandler", "ErrorHandler").
Default: workspace only. Set include_external=true for stdlib/dependencies.

Returns: List of matching symbols with kind, location, and container.`,
	}, s.SearchSymbols)

	sdk.AddTool(server, &sdk.Tool{
		Name: "get_hover",
		Description: `Get type information and documentation for a symbol.

Use this to:
- View function signatures and parameter types
- Read documentation comments (godoc)
- Understand what a type or variable represents

Recommended usage: file_path + symbol + use_disk=true
Alternative: file_path + line/col + code

Returns: Type signature and documentation text.`,
	}, s.GetHover)

	sdk.AddTool(server, &sdk.Tool{
		Name: "explain_symbol",
		Description: `Get comprehensive information about a symbol in one call.

Combines definition, hover, and references into a single response. This is the recommended
tool for understanding a symbol - more efficient than calling multiple tools separately.

Use this to:
- Understand what a function/type/variable does
- See its signature, documentation, and source code
- Find where it's used across the codebase

Returns: Symbol name, kind, signature, documentation, source code, definition location, and references.`,
	}, s.ExplainSymbol)

	server.AddResource(&sdk.Resource{
		URI:         "byte-lsp://about",
		Name:        "byte-lsp-mcp",
		Title:       "Byte LSP MCP Server",
		Description: "gopls-based Go analysis tools (diagnostics, definition, references, hover, symbol search).",
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

func (s *Service) AnalyzeCode(ctx context.Context, _ *sdk.CallToolRequest, input tools.AnalyzeCodeInput) (*sdk.CallToolResult, tools.AnalyzeCodeOutput, error) {
	if input.Code == "" || input.FilePath == "" {
		return nil, tools.AnalyzeCodeOutput{}, errors.New("code and file_path are required")
	}
	if err := s.Initialize(ctx); err != nil {
		return nil, tools.AnalyzeCodeOutput{}, err
	}

	absPath, uri, err := s.prepareDocument(ctx, input.FilePath, input.Code)
	if err != nil {
		return nil, tools.AnalyzeCodeOutput{}, err
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

	return nil, tools.AnalyzeCodeOutput{FilePath: absPath, Diagnostics: diagnostics}, nil
}

func (s *Service) GoToDefinition(ctx context.Context, _ *sdk.CallToolRequest, input tools.GoToDefinitionInput) (*sdk.CallToolResult, tools.GoToDefinitionOutput, error) {
	if input.FilePath == "" {
		return nil, tools.GoToDefinitionOutput{}, errors.New("file_path is required")
	}
	if err := s.Initialize(ctx); err != nil {
		return nil, tools.GoToDefinitionOutput{}, err
	}

	code := input.Code
	absPath := ""
	if input.UseDisk || code == "" {
		path, err := s.resolveDiskPath(input.FilePath)
		if err != nil {
			if input.UseDisk || code == "" {
				return nil, tools.GoToDefinitionOutput{}, err
			}
		} else {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, tools.GoToDefinitionOutput{}, err
			}
			code = string(data)
			absPath = path
		}
	}
	if code == "" {
		return nil, tools.GoToDefinitionOutput{}, errors.New("code is required (or set use_disk to read from file_path)")
	}

	line := input.Line
	col := input.Col
	if input.Symbol != "" {
		if sl, sc, ok := tools.FindSymbolPosition(code, input.Symbol); ok {
			line, col = sl, sc
		} else {
			return nil, tools.GoToDefinitionOutput{}, errors.New("symbol not found in provided code")
		}
	} else if line < 1 || col < 1 {
		return nil, tools.GoToDefinitionOutput{}, errors.New("line and col are required when symbol is not provided")
	}

	filePath := input.FilePath
	if absPath != "" {
		filePath = absPath
	}
	_, uri, err := s.prepareDocument(ctx, filePath, code)
	if err != nil {
		return nil, tools.GoToDefinitionOutput{}, err
	}
	s.warmupDocument(ctx, uri)

	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri,
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": col - 1,
		},
	}
	raw, err := s.client.SendRequest(ctx, "textDocument/definition", params)
	if err != nil && shouldAdjustPosition(err) {
		if nl, nc, ok := adjustPositionFromCode(code, line, col); ok {
			params["position"] = map[string]interface{}{
				"line":      nl - 1,
				"character": nc - 1,
			}
			raw, err = s.client.SendRequest(ctx, "textDocument/definition", params)
		}
	}
	if err != nil {
		return nil, tools.GoToDefinitionOutput{}, err
	}
	locs, err := tools.ParseLocations(raw)
	if err != nil {
		return nil, tools.GoToDefinitionOutput{}, err
	}
	return nil, tools.GoToDefinitionOutput{Locations: locs}, nil
}

func (s *Service) FindReferences(ctx context.Context, _ *sdk.CallToolRequest, input tools.FindReferencesInput) (*sdk.CallToolResult, tools.FindReferencesOutput, error) {
	if input.FilePath == "" {
		return nil, tools.FindReferencesOutput{}, errors.New("file_path is required")
	}
	if err := s.Initialize(ctx); err != nil {
		return nil, tools.FindReferencesOutput{}, err
	}

	code := input.Code
	absPath := ""
	if input.UseDisk || code == "" {
		path, err := s.resolveDiskPath(input.FilePath)
		if err != nil {
			if input.UseDisk || code == "" {
				return nil, tools.FindReferencesOutput{}, err
			}
		} else {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, tools.FindReferencesOutput{}, err
			}
			code = string(data)
			absPath = path
		}
	}
	if code == "" {
		return nil, tools.FindReferencesOutput{}, errors.New("code is required (or set use_disk to read from file_path)")
	}

	line := input.Line
	col := input.Col
	if input.Symbol != "" {
		if sl, sc, ok := tools.FindSymbolPosition(code, input.Symbol); ok {
			line, col = sl, sc
		} else {
			return nil, tools.FindReferencesOutput{}, errors.New("symbol not found in provided code")
		}
	} else if line < 1 || col < 1 {
		return nil, tools.FindReferencesOutput{}, errors.New("line and col are required when symbol is not provided")
	}

	filePath := input.FilePath
	if absPath != "" {
		filePath = absPath
	}
	_, uri, err := s.prepareDocument(ctx, filePath, code)
	if err != nil {
		return nil, tools.FindReferencesOutput{}, err
	}
	s.warmupDocument(ctx, uri)

	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri,
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": col - 1,
		},
		"context": map[string]interface{}{
			"includeDeclaration": input.IncludeDeclaration,
		},
	}
	raw, err := s.client.SendRequest(ctx, "textDocument/references", params)
	if err != nil && shouldAdjustPosition(err) {
		if nl, nc, ok := adjustPositionFromCode(code, line, col); ok {
			params["position"] = map[string]interface{}{
				"line":      nl - 1,
				"character": nc - 1,
			}
			raw, err = s.client.SendRequest(ctx, "textDocument/references", params)
		}
	}
	if err != nil {
		return nil, tools.FindReferencesOutput{}, err
	}
	locs, err := tools.ParseLocations(raw)
	if err != nil {
		return nil, tools.FindReferencesOutput{}, err
	}

	refs := make([]tools.ReferenceResult, 0, len(locs))
	for _, loc := range locs {
		refs = append(refs, tools.ReferenceResult{Location: loc})
	}
	return nil, tools.FindReferencesOutput{References: refs}, nil
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

func (s *Service) GetHover(ctx context.Context, _ *sdk.CallToolRequest, input tools.GetHoverInput) (*sdk.CallToolResult, tools.GetHoverOutput, error) {
	if input.FilePath == "" {
		return nil, tools.GetHoverOutput{}, errors.New("file_path is required")
	}
	if err := s.Initialize(ctx); err != nil {
		return nil, tools.GetHoverOutput{}, err
	}

	code := input.Code
	absPath := ""
	if input.UseDisk || code == "" {
		path, err := s.resolveDiskPath(input.FilePath)
		if err != nil {
			if input.UseDisk || code == "" {
				return nil, tools.GetHoverOutput{}, err
			}
		} else {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, tools.GetHoverOutput{}, err
			}
			code = string(data)
			absPath = path
		}
	}
	if code == "" {
		return nil, tools.GetHoverOutput{}, errors.New("code is required (or set use_disk to read from file_path)")
	}

	line := input.Line
	col := input.Col
	if input.Symbol != "" {
		if sl, sc, ok := tools.FindSymbolPosition(code, input.Symbol); ok {
			line, col = sl, sc
		} else {
			return nil, tools.GetHoverOutput{}, errors.New("symbol not found in provided code")
		}
	} else if line < 1 || col < 1 {
		return nil, tools.GetHoverOutput{}, errors.New("line and col are required when symbol is not provided")
	}

	filePath := input.FilePath
	if absPath != "" {
		filePath = absPath
	}
	_, uri, err := s.prepareDocument(ctx, filePath, code)
	if err != nil {
		return nil, tools.GetHoverOutput{}, err
	}
	s.warmupDocument(ctx, uri)

	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri,
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": col - 1,
		},
	}
	raw, err := s.client.SendRequest(ctx, "textDocument/hover", params)
	if err != nil && shouldAdjustPosition(err) {
		if nl, nc, ok := adjustPositionFromCode(code, line, col); ok {
			params["position"] = map[string]interface{}{
				"line":      nl - 1,
				"character": nc - 1,
			}
			raw, err = s.client.SendRequest(ctx, "textDocument/hover", params)
		}
	}
	if err != nil {
		return nil, tools.GetHoverOutput{}, err
	}
	out, err := tools.ParseHover(raw)
	if err != nil {
		return nil, tools.GetHoverOutput{}, err
	}
	return nil, out, nil
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

func filterErrors(diags []tools.Diagnostic) []tools.Diagnostic {
	out := make([]tools.Diagnostic, 0, len(diags))
	for _, d := range diags {
		if d.Severity == "error" {
			out = append(out, d)
		}
	}
	return out
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
