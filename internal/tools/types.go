package tools

// AnalyzeCodeInput for analyze_code.
type AnalyzeCodeInput struct {
	Code            string `json:"code" jsonschema:"required,description=Go source code to analyze"`
	FilePath        string `json:"file_path" jsonschema:"required,description=File path (absolute or workspace-relative). Used for module context resolution."`
	IncludeWarnings bool   `json:"include_warnings,omitempty" jsonschema:"description=Include warnings/info/hints in addition to errors. Default: false (errors only)."`
}

type Diagnostic struct {
	Line       int    `json:"line"`
	Col        int    `json:"col"`
	EndLine    int    `json:"end_line"`
	EndCol     int    `json:"end_col"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	Code       string `json:"code,omitempty"`
	Source     string `json:"source,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

type AnalyzeCodeOutput struct {
	FilePath    string       `json:"file_path"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// GoToDefinitionInput for go_to_definition.
type GoToDefinitionInput struct {
	FilePath string `json:"file_path" jsonschema:"required,description=File path (absolute or workspace-relative) where the symbol is located."`
	Symbol   string `json:"symbol,omitempty" jsonschema:"description=Symbol name (function/type/variable) to find. Recommended: simpler than specifying line/col."`
	Line     int    `json:"line,omitempty" jsonschema:"description=1-based line number. Required if symbol is not provided."`
	Col      int    `json:"col,omitempty" jsonschema:"description=1-based column number. Required if symbol is not provided."`
	Code     string `json:"code,omitempty" jsonschema:"description=Go source code content. Required unless use_disk=true."`
	UseDisk  bool   `json:"use_disk,omitempty" jsonschema:"description=Read file content from disk instead of code parameter. Recommended for existing files."`
}

type Location struct {
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	EndLine  int    `json:"end_line,omitempty"`
	EndCol   int    `json:"end_col,omitempty"`
}

type GoToDefinitionOutput struct {
	Locations []Location `json:"locations"`
}

// FindReferencesInput for find_references.
type FindReferencesInput struct {
	FilePath           string `json:"file_path" jsonschema:"required,description=File path (absolute or workspace-relative) where the symbol is located."`
	Symbol             string `json:"symbol,omitempty" jsonschema:"description=Symbol name (function/type/variable) to find. Recommended: simpler than specifying line/col."`
	Line               int    `json:"line,omitempty" jsonschema:"description=1-based line number. Required if symbol is not provided."`
	Col                int    `json:"col,omitempty" jsonschema:"description=1-based column number. Required if symbol is not provided."`
	Code               string `json:"code,omitempty" jsonschema:"description=Go source code content. Required unless use_disk=true."`
	UseDisk            bool   `json:"use_disk,omitempty" jsonschema:"description=Read file content from disk instead of code parameter. Recommended for existing files."`
	IncludeDeclaration bool   `json:"include_declaration,omitempty" jsonschema:"description=Include the symbol declaration in results. Default: false."`
}

type ReferenceResult struct {
	Location     Location `json:"location"`
	IsDefinition bool     `json:"is_definition,omitempty"`
}

type FindReferencesOutput struct {
	References []ReferenceResult `json:"references"`
}

// SearchSymbolsInput for search_symbols.
type SearchSymbolsInput struct {
	Query           string `json:"query" jsonschema:"required,description=Symbol name or pattern to search (e.g. 'Handler' or 'New*')."`
	IncludeExternal bool   `json:"include_external,omitempty" jsonschema:"description=Include symbols from stdlib and dependencies. Default: false (workspace only)."`
}

type SymbolInformation struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path,omitempty"`
	Line          int    `json:"line,omitempty"`
	Col           int    `json:"col,omitempty"`
	ContainerName string `json:"container_name,omitempty"`
}

type SearchSymbolsOutput struct {
	Symbols []SymbolInformation `json:"symbols"`
}

// GetHoverInput for get_hover.
type GetHoverInput struct {
	FilePath string `json:"file_path" jsonschema:"required,description=File path (absolute or workspace-relative) where the symbol is located."`
	Symbol   string `json:"symbol,omitempty" jsonschema:"description=Symbol name (function/type/variable) to find. Recommended: simpler than specifying line/col."`
	Line     int    `json:"line,omitempty" jsonschema:"description=1-based line number. Required if symbol is not provided."`
	Col      int    `json:"col,omitempty" jsonschema:"description=1-based column number. Required if symbol is not provided."`
	Code     string `json:"code,omitempty" jsonschema:"description=Go source code content. Required unless use_disk=true."`
	UseDisk  bool   `json:"use_disk,omitempty" jsonschema:"description=Read file content from disk instead of code parameter. Recommended for existing files."`
}

type GetHoverOutput struct {
	Contents string    `json:"contents"`
	Range    *Location `json:"range,omitempty"`
}

// ExplainSymbolInput for explain_symbol.
type ExplainSymbolInput struct {
	FilePath          string `json:"file_path" jsonschema:"required,description=File path (absolute or workspace-relative) where the symbol is located."`
	Symbol            string `json:"symbol" jsonschema:"required,description=Symbol name (function/type/variable/method) to explain."`
	IncludeSource     bool   `json:"include_source,omitempty" jsonschema:"description=Include the source code of the symbol definition. Default: true."`
	IncludeReferences bool   `json:"include_references,omitempty" jsonschema:"description=Include references to this symbol. Default: true."`
	MaxReferences     int    `json:"max_references,omitempty" jsonschema:"description=Maximum number of references to return. Default: 10."`
}

// ReferenceContext contains a reference with surrounding context.
type ReferenceContext struct {
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Context  string `json:"context,omitempty"` // The line of code containing the reference
}

// ExplainSymbolOutput contains comprehensive information about a symbol.
type ExplainSymbolOutput struct {
	Name            string             `json:"name"`
	Kind            string             `json:"kind"`                       // Function, Method, Type, Variable, Constant, etc.
	Signature       string             `json:"signature,omitempty"`        // Full type signature
	Doc             string             `json:"doc,omitempty"`              // Documentation comment
	Source          string             `json:"source,omitempty"`           // Source code of the definition
	DefinedAt       *Location          `json:"defined_at,omitempty"`       // Where the symbol is defined
	ReferencesCount int                `json:"references_count,omitempty"` // Total number of references
	References      []ReferenceContext `json:"references,omitempty"`       // Sample references with context
}
