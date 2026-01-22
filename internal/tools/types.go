package tools

// AnalyzeCodeInput for analyze_code.
type AnalyzeCodeInput struct {
	Code            string `json:"code"`
	FilePath        string `json:"file_path"`
	IncludeWarnings bool   `json:"include_warnings"`
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
	Code     string `json:"code"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
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
	Code               string `json:"code"`
	FilePath           string `json:"file_path"`
	Line               int    `json:"line"`
	Col                int    `json:"col"`
	IncludeDeclaration bool   `json:"include_declaration"`
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
	Query           string `json:"query"`
	IncludeExternal bool   `json:"include_external"`
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
	Code     string `json:"code"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
}

type GetHoverOutput struct {
	Contents string    `json:"contents"`
	Range    *Location `json:"range,omitempty"`
}
