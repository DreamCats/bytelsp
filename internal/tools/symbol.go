package tools

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// FindSymbolPosition returns the 1-based line/column for the first matching declaration of symbol.
func FindSymbolPosition(code, symbol string) (int, int, bool) {
	if code == "" || symbol == "" {
		return 0, 0, false
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "symbol.go", code, parser.ParseComments)
	if err == nil {
		if line, col, ok := findDeclPosition(fset, file, symbol); ok {
			return line, col, true
		}
		if line, col, ok := findFirstIdentPosition(fset, file, symbol); ok {
			return line, col, true
		}
	}

	return findSymbolInText(code, symbol)
}

func findDeclPosition(fset *token.FileSet, file *ast.File, symbol string) (int, int, bool) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name != nil && d.Name.Name == symbol {
				pos := fset.Position(d.Name.Pos())
				return pos.Line, pos.Column, true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name != nil && s.Name.Name == symbol {
						pos := fset.Position(s.Name.Pos())
						return pos.Line, pos.Column, true
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name != nil && name.Name == symbol {
							pos := fset.Position(name.Pos())
							return pos.Line, pos.Column, true
						}
					}
				}
			}
		}
	}
	return 0, 0, false
}

func findFirstIdentPosition(fset *token.FileSet, file *ast.File, symbol string) (int, int, bool) {
	var found *ast.Ident
	ast.Inspect(file, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if id.Name == symbol {
			found = id
			return false
		}
		return true
	})
	if found != nil {
		pos := fset.Position(found.Pos())
		return pos.Line, pos.Column, true
	}
	return 0, 0, false
}

func findSymbolInText(code, symbol string) (int, int, bool) {
	start := 0
	for {
		idx := strings.Index(code[start:], symbol)
		if idx < 0 {
			return 0, 0, false
		}
		idx += start
		if isSymbolBoundary(code, idx, len(symbol)) {
			line, col := positionFromIndex(code, idx)
			return line, col, true
		}
		start = idx + len(symbol)
	}
}

func isSymbolBoundary(code string, idx, length int) bool {
	if idx > 0 {
		if isIdentChar(code[idx-1]) {
			return false
		}
	}
	if idx+length < len(code) {
		if isIdentChar(code[idx+length]) {
			return false
		}
	}
	return true
}

func isIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func positionFromIndex(code string, idx int) (int, int) {
	line := 1
	col := 1
	for i := 0; i < idx && i < len(code); i++ {
		if code[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}
