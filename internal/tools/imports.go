package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ModuleInfo represents go list -m -json output.
type ModuleInfo struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Dir     string `json:"Dir"`
}

// PackageInfo represents go list -json output.
type PackageInfo struct {
	Dir        string `json:"Dir"`
	ImportPath string `json:"ImportPath"`
	GoFiles    []string `json:"GoFiles"`
	Module     *ModuleInfo `json:"Module"`
}

// ResolveImportPath resolves an import path to its directory on disk.
func ResolveImportPath(workdir, importPath string) (*PackageInfo, error) {
	cmd := exec.Command("go", "list", "-json", importPath)
	cmd.Dir = workdir
	cmd.Env = os.Environ()

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list %s: %w", importPath, err)
	}

	var pkg PackageInfo
	if err := json.Unmarshal(out, &pkg); err != nil {
		return nil, fmt.Errorf("parse go list output: %w", err)
	}

	return &pkg, nil
}

// ParseSymbolFromPackage parses a symbol from a package directory.
func ParseSymbolFromPackage(pkgDir string, goFiles []string, symbolName string) (*ExplainImportOutput, error) {
	fset := token.NewFileSet()

	// Parse all Go files in the package
	var files []*ast.File
	for _, filename := range goFiles {
		fullPath := filepath.Join(pkgDir, filename)
		f, err := parser.ParseFile(fset, fullPath, nil, parser.ParseComments)
		if err != nil {
			continue // Skip files that fail to parse
		}
		files = append(files, f)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no parseable Go files found")
	}

	// Search for the symbol in all files
	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				result := parseGenDecl(fset, d, symbolName)
				if result != nil {
					pos := fset.Position(d.Pos())
					result.FilePath = pos.Filename
					result.Line = pos.Line
					return result, nil
				}
			case *ast.FuncDecl:
				if d.Name.Name == symbolName {
					result := parseFuncDecl(fset, d)
					pos := fset.Position(d.Pos())
					result.FilePath = pos.Filename
					result.Line = pos.Line
					return result, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("symbol %q not found in package", symbolName)
}

func parseGenDecl(fset *token.FileSet, decl *ast.GenDecl, symbolName string) *ExplainImportOutput {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if s.Name.Name != symbolName {
				continue
			}
			return parseTypeSpec(fset, decl, s)

		case *ast.ValueSpec:
			for _, name := range s.Names {
				if name.Name == symbolName {
					return parseValueSpec(fset, decl, s, name.Name)
				}
			}
		}
	}
	return nil
}

func parseTypeSpec(fset *token.FileSet, decl *ast.GenDecl, spec *ast.TypeSpec) *ExplainImportOutput {
	result := &ExplainImportOutput{
		Symbol: spec.Name.Name,
	}

	// Get documentation
	if spec.Doc != nil {
		result.Doc = spec.Doc.Text()
	} else if decl.Doc != nil {
		result.Doc = decl.Doc.Text()
	}

	switch t := spec.Type.(type) {
	case *ast.StructType:
		result.Kind = "Struct"
		result.Signature = formatNode(fset, spec)
		result.Fields = parseStructFields(fset, t)

	case *ast.InterfaceType:
		result.Kind = "Interface"
		result.Signature = formatNode(fset, spec)
		result.Methods = parseInterfaceMethods(fset, t)

	case *ast.Ident, *ast.SelectorExpr, *ast.ArrayType, *ast.MapType, *ast.ChanType, *ast.FuncType:
		result.Kind = "Type"
		result.Signature = formatNode(fset, spec)

	default:
		result.Kind = "Type"
		result.Signature = formatNode(fset, spec)
	}

	return result
}

func parseStructFields(fset *token.FileSet, st *ast.StructType) []FieldInfo {
	if st.Fields == nil {
		return nil
	}

	var fields []FieldInfo
	for _, field := range st.Fields.List {
		typeStr := formatNode(fset, field.Type)
		tagStr := ""
		if field.Tag != nil {
			tagStr = field.Tag.Value
		}
		docStr := ""
		if field.Doc != nil {
			docStr = strings.TrimSpace(field.Doc.Text())
		} else if field.Comment != nil {
			docStr = strings.TrimSpace(field.Comment.Text())
		}

		if len(field.Names) == 0 {
			// Embedded field
			fields = append(fields, FieldInfo{
				Name: typeStr, // Embedded type name
				Type: typeStr,
				Tag:  tagStr,
				Doc:  docStr,
			})
		} else {
			for _, name := range field.Names {
				fields = append(fields, FieldInfo{
					Name: name.Name,
					Type: typeStr,
					Tag:  tagStr,
					Doc:  docStr,
				})
			}
		}
	}
	return fields
}

func parseInterfaceMethods(fset *token.FileSet, it *ast.InterfaceType) []string {
	if it.Methods == nil {
		return nil
	}

	var methods []string
	for _, method := range it.Methods.List {
		if len(method.Names) > 0 {
			// Named method
			sig := formatNode(fset, method)
			methods = append(methods, sig)
		} else {
			// Embedded interface
			methods = append(methods, formatNode(fset, method.Type))
		}
	}
	return methods
}

func parseValueSpec(fset *token.FileSet, decl *ast.GenDecl, spec *ast.ValueSpec, name string) *ExplainImportOutput {
	result := &ExplainImportOutput{
		Symbol: name,
	}

	switch decl.Tok {
	case token.CONST:
		result.Kind = "Const"
	case token.VAR:
		result.Kind = "Var"
	}

	if spec.Doc != nil {
		result.Doc = spec.Doc.Text()
	} else if decl.Doc != nil {
		result.Doc = decl.Doc.Text()
	}

	// Build signature
	var sig strings.Builder
	sig.WriteString(decl.Tok.String())
	sig.WriteString(" ")
	sig.WriteString(name)
	if spec.Type != nil {
		sig.WriteString(" ")
		sig.WriteString(formatNode(fset, spec.Type))
	}
	if len(spec.Values) > 0 {
		sig.WriteString(" = ")
		for i, v := range spec.Values {
			if i > 0 {
				sig.WriteString(", ")
			}
			sig.WriteString(formatNode(fset, v))
		}
	}
	result.Signature = sig.String()

	return result
}

func parseFuncDecl(fset *token.FileSet, decl *ast.FuncDecl) *ExplainImportOutput {
	result := &ExplainImportOutput{
		Symbol: decl.Name.Name,
		Kind:   "Function",
	}

	if decl.Recv != nil {
		result.Kind = "Method"
	}

	if decl.Doc != nil {
		result.Doc = decl.Doc.Text()
	}

	// Build signature (without body)
	var sig strings.Builder
	sig.WriteString("func ")
	if decl.Recv != nil {
		sig.WriteString("(")
		sig.WriteString(formatNode(fset, decl.Recv))
		sig.WriteString(") ")
	}
	sig.WriteString(decl.Name.Name)
	// formatNode for FuncType outputs "func(...)", we need just the params part
	funcType := formatNode(fset, decl.Type)
	if strings.HasPrefix(funcType, "func") {
		funcType = funcType[4:] // Remove "func" prefix
	}
	sig.WriteString(funcType)
	result.Signature = sig.String()

	return result
}

func formatNode(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	printer.Fprint(&buf, fset, node)
	return buf.String()
}
