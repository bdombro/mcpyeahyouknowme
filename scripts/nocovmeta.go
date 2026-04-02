// Command nocovmeta prints tab-separated metadata for scripts/test.sh when filtering
// coverage profiles. The awk step in test.sh loads this stream to drop uncovered
// regions that include a // nocov comment or sit inside a // nocov-marked func body.
//
// Record format (tab-separated, stdout):
//
//	line <covPath> <line>     — physical line contains the substring "// nocov"
//	func <covPath> <start> <end> — same line starts with "func " after leading space;
//	  start is the func keyword line; end is the closing brace line of the body (AST).
//
// covPath is always mcpyeahyouknowme/<relative path from cli_dir using forward slashes>.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// modulePrefix matches the package segment used in go test -coverprofile paths.
const modulePrefix = "mcpyeahyouknowme/"

// main expects one argument: absolute or relative path to the Go module root (src/).
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: nocovmeta <cli_dir>\n")
		os.Exit(2)
	}
	cliDir, err := filepath.Abs(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "nocovmeta: %v\n", err)
		os.Exit(1)
	}

	// Globs mirror the packages that contribute to the filtered coverage report.
	patterns := []string{
		"*.go",
		"sources/whatsapp/*.go",
		"sources/gsuite/*.go",
		"sources/google_places/*.go",
		"sources/notebook/*.go",
	}

	seen := make(map[string]struct{}) // paths can appear in more than one pattern
	var paths []string
	for _, pat := range patterns {
		matches, err := filepath.Glob(filepath.Join(cliDir, pat))
		if err != nil {
			fmt.Fprintf(os.Stderr, "nocovmeta: glob %q: %v\n", pat, err)
			os.Exit(1)
		}
		for _, p := range matches {
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			base := filepath.Base(p)
			if strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_init.go") {
				continue
			}
			paths = append(paths, p)
		}
	}
	sort.Strings(paths) // stable output for diffs and debugging

	for _, path := range paths {
		if err := processFile(cliDir, path); err != nil {
			fmt.Fprintf(os.Stderr, "nocovmeta: %s: %v\n", path, err)
			os.Exit(1)
		}
	}
}

// processFile reads one .go file, parses it when possible, and prints nocov records to stdout.
func processFile(cliDir, path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(cliDir, path)
	if err != nil {
		return err
	}
	covPath := modulePrefix + filepath.ToSlash(rel)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		// Broken or non-Go text: still honor line-level // nocov; skip func ranges.
		emitLineRecords(covPath, src, nil)
		return nil
	}

	// FuncDecl.Pos() is the func keyword; value is the body's closing brace line.
	funcEndByDeclLine := map[int]int{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		line := fset.Position(fn.Pos()).Line
		funcEndByDeclLine[line] = fset.Position(fn.Body.Rbrace).Line
	}

	emitLineRecords(covPath, src, funcEndByDeclLine)
	return nil
}

// emitLineRecords scans source lines (1-based line numbers). funcEndByDeclLine is nil
// when the file was not parsed; otherwise it maps func-keyword line → body rbrace line.
func emitLineRecords(covPath string, src []byte, funcEndByDeclLine map[int]int) {
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	for i, line := range lines {
		lineNum := i + 1
		if !strings.Contains(line, "// nocov") {
			continue
		}
		fmt.Printf("line\t%s\t%d\n", covPath, lineNum)
		// Only top-level func declarations on the same line as // nocov get a func record
		// (same rule as the old shell/Python filter: line comment + "func " prefix).
		trim := strings.TrimLeftFunc(line, unicode.IsSpace)
		if !strings.HasPrefix(trim, "func ") {
			continue
		}
		if funcEndByDeclLine == nil {
			continue
		}
		if endLine, ok := funcEndByDeclLine[lineNum]; ok {
			fmt.Printf("func\t%s\t%d\t%d\n", covPath, lineNum, endLine)
		}
	}
}
