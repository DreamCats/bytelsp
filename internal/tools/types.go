package tools

// AnalyzeCodeInput for analyze_code.
type AnalyzeCodeInput struct {
	Code            string `json:"code" jsonschema:"Go source code content"`
	FilePath        string `json:"file_path" jsonschema:"File path for the code (absolute or workspace-relative)"`
	IncludeWarnings bool   `json:"include_warnings" jsonschema:"Include warnings/info/hints in diagnostics (default: false)"`
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
	Code     string `json:"code" jsonschema:"Go source code content"`
	FilePath string `json:"file_path" jsonschema:"File path for the code (absolute or workspace-relative)"`
	Symbol   string `json:"symbol,omitempty" jsonschema:"Optional symbol name to locate in the file (overrides line/col)"`
	Line     int    `json:"line" jsonschema:"1-based line number"`
	Col      int    `json:"col" jsonschema:"1-based column number"`
	UseDisk  bool   `json:"use_disk" jsonschema:"Read code from file_path on disk instead of the provided code (default: false)"`
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
	Code               string `json:"code" jsonschema:"Go source code content"`
	FilePath           string `json:"file_path" jsonschema:"File path for the code (absolute or workspace-relative)"`
	Symbol             string `json:"symbol,omitempty" jsonschema:"Optional symbol name to locate in the file (overrides line/col)"`
	Line               int    `json:"line" jsonschema:"1-based line number"`
	Col                int    `json:"col" jsonschema:"1-based column number"`
	IncludeDeclaration bool   `json:"include_declaration" jsonschema:"Include declaration in results (default: false)"`
	UseDisk            bool   `json:"use_disk" jsonschema:"Read code from file_path on disk instead of the provided code (default: false)"`
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
	Query           string `json:"query" jsonschema:"Symbol name or pattern"`
	IncludeExternal bool   `json:"include_external" jsonschema:"Include symbols outside the workspace (stdlib/module cache) (default: false)"`
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
	Code     string `json:"code" jsonschema:"Go source code content"`
	FilePath string `json:"file_path" jsonschema:"File path for the code (absolute or workspace-relative)"`
	Line     int    `json:"line" jsonschema:"1-based line number"`
	Col      int    `json:"col" jsonschema:"1-based column number"`
}

type GetHoverOutput struct {
	Contents string    `json:"contents"`
	Range    *Location `json:"range,omitempty"`
}
