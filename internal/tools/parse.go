package tools

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspDiagnostic struct {
	Range    lspRange    `json:"range"`
	Severity int         `json:"severity"`
	Message  string      `json:"message"`
	Source   string      `json:"source"`
	Code     interface{} `json:"code"`
}

type diagnosticReport struct {
	Items            []lspDiagnostic                 `json:"items"`
	RelatedDocuments map[string]diagnosticDocSection `json:"relatedDocuments"`
	Kind             string                          `json:"kind"`
}

type diagnosticDocSection struct {
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspLocationLink struct {
	TargetURI   string   `json:"targetUri"`
	TargetRange lspRange `json:"targetRange"`
}

type lspHover struct {
	Contents interface{} `json:"contents"`
	Range    *lspRange   `json:"range"`
}

type lspSymbolInformation struct {
	Name          string      `json:"name"`
	Kind          int         `json:"kind"`
	Location      lspLocation `json:"location"`
	ContainerName string      `json:"containerName"`
}

func ParseDiagnostics(raw json.RawMessage, uri string) ([]Diagnostic, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []Diagnostic{}, nil
	}

	var report diagnosticReport
	if err := json.Unmarshal(raw, &report); err == nil {
		if len(report.RelatedDocuments) > 0 {
			var diags []Diagnostic
			for docURI, section := range report.RelatedDocuments {
				if uri != "" && docURI != uri {
					continue
				}
				diags = append(diags, convertDiagnostics(section.Diagnostics)...)
			}
			return diags, nil
		}
		if len(report.Items) > 0 {
			return convertDiagnostics(report.Items), nil
		}
	}

	var arr []lspDiagnostic
	if err := json.Unmarshal(raw, &arr); err == nil {
		return convertDiagnostics(arr), nil
	}

	return nil, fmt.Errorf("unsupported diagnostics format")
}

func convertDiagnostics(items []lspDiagnostic) []Diagnostic {
	out := make([]Diagnostic, 0, len(items))
	for _, d := range items {
		sev := "error"
		switch d.Severity {
		case 1:
			sev = "error"
		case 2:
			sev = "warning"
		case 3:
			sev = "info"
		case 4:
			sev = "hint"
		}
		code := ""
		switch v := d.Code.(type) {
		case string:
			code = v
		case float64:
			code = fmt.Sprintf("%g", v)
		case json.Number:
			code = v.String()
		}
		out = append(out, Diagnostic{
			Line:     d.Range.Start.Line + 1,
			Col:      d.Range.Start.Character + 1,
			EndLine:  d.Range.End.Line + 1,
			EndCol:   d.Range.End.Character + 1,
			Severity: sev,
			Message:  d.Message,
			Source:   d.Source,
			Code:     code,
		})
	}
	return out
}

func ParseLocations(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []Location{}, nil
	}

	var many []lspLocation
	if err := json.Unmarshal(raw, &many); err == nil {
		return convertLocations(many), nil
	}

	var single lspLocation
	if err := json.Unmarshal(raw, &single); err == nil {
		return convertLocations([]lspLocation{single}), nil
	}

	var links []lspLocationLink
	if err := json.Unmarshal(raw, &links); err == nil {
		return convertLocationLinks(links), nil
	}

	return nil, fmt.Errorf("unsupported location format")
}

func convertLocations(items []lspLocation) []Location {
	out := make([]Location, 0, len(items))
	for _, loc := range items {
		out = append(out, Location{
			FilePath: uriToPath(loc.URI),
			Line:     loc.Range.Start.Line + 1,
			Col:      loc.Range.Start.Character + 1,
			EndLine:  loc.Range.End.Line + 1,
			EndCol:   loc.Range.End.Character + 1,
		})
	}
	return out
}

func convertLocationLinks(items []lspLocationLink) []Location {
	out := make([]Location, 0, len(items))
	for _, loc := range items {
		out = append(out, Location{
			FilePath: uriToPath(loc.TargetURI),
			Line:     loc.TargetRange.Start.Line + 1,
			Col:      loc.TargetRange.Start.Character + 1,
			EndLine:  loc.TargetRange.End.Line + 1,
			EndCol:   loc.TargetRange.End.Character + 1,
		})
	}
	return out
}

func ParseHover(raw json.RawMessage) (GetHoverOutput, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return GetHoverOutput{}, nil
	}
	var h lspHover
	if err := json.Unmarshal(raw, &h); err != nil {
		return GetHoverOutput{}, err
	}

	contents := renderHoverContents(h.Contents)
	var rng *Location
	if h.Range != nil {
		rng = &Location{
			Line:    h.Range.Start.Line + 1,
			Col:     h.Range.Start.Character + 1,
			EndLine: h.Range.End.Line + 1,
			EndCol:  h.Range.End.Character + 1,
		}
	}
	return GetHoverOutput{Contents: contents, Range: rng}, nil
}

func renderHoverContents(contents interface{}) string {
	switch v := contents.(type) {
	case string:
		return v
	case map[string]interface{}:
		if val, ok := v["value"].(string); ok {
			return val
		}
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, renderHoverContents(item))
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func ParseSymbols(raw json.RawMessage) ([]SymbolInformation, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []SymbolInformation{}, nil
	}
	var items []lspSymbolInformation
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	out := make([]SymbolInformation, 0, len(items))
	for _, sym := range items {
		out = append(out, SymbolInformation{
			Name:          sym.Name,
			Kind:          symbolKindToString(sym.Kind),
			FilePath:      uriToPath(sym.Location.URI),
			Line:          sym.Location.Range.Start.Line + 1,
			Col:           sym.Location.Range.Start.Character + 1,
			ContainerName: sym.ContainerName,
		})
	}
	return out, nil
}

func symbolKindToString(kind int) string {
	switch kind {
	case 1:
		return "File"
	case 2:
		return "Module"
	case 3:
		return "Namespace"
	case 4:
		return "Package"
	case 5:
		return "Class"
	case 6:
		return "Method"
	case 7:
		return "Property"
	case 8:
		return "Field"
	case 9:
		return "Constructor"
	case 10:
		return "Enum"
	case 11:
		return "Interface"
	case 12:
		return "Function"
	case 13:
		return "Variable"
	case 14:
		return "Constant"
	case 15:
		return "String"
	case 16:
		return "Number"
	case 17:
		return "Boolean"
	case 18:
		return "Array"
	case 19:
		return "Object"
	case 20:
		return "Key"
	case 21:
		return "Null"
	case 22:
		return "EnumMember"
	case 23:
		return "Struct"
	case 24:
		return "Event"
	case 25:
		return "Operator"
	case 26:
		return "TypeParameter"
	default:
		return "Unknown"
	}
}

func uriToPath(uri string) string {
	if uri == "" {
		return ""
	}
	parsed, err := url.Parse(uri)
	if err == nil && parsed.Scheme == "file" {
		path := parsed.Path
		if path == "" {
			return uri
		}
		return filepath.FromSlash(path)
	}
	return uri
}
