// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package unbounded finds two shapes in Go source: a ZIP entry read whole with
// no ceiling, and an image decoded without anyone having looked at its header
// first.
//
// Both are how a few hundred bytes of input become a few gigabytes of memory,
// and both turned up independently in three repositories of this fleet on
// 2026-10-09 — in go-odf/odf, in go-pdfkit/convert, and, found by this, in
// go-gfx/gfx, where a 246-byte SVG held 1.5 GiB.
//
// # It is a lead, not a verdict
//
// Both shapes are perfectly correct over input the program itself produced: a
// writer embedding its own logo decodes a PNG it wrote. Every hit has to be
// read by somebody who knows where the bytes came from. What this owes in
// return is to find them all, and to say plainly what it could NOT read —
// because a sweep that prints nothing is indistinguishable from a sweep that
// could not run.
//
// # Why it exists at all
//
// A fix does not travel. go-richdoc/latex was corrected and its sibling
// go-richdoc/rst carried the identical defect for weeks, untouched, because
// nobody looked. The rule that came out of that is to sweep the siblings; this
// is that rule with a program behind it instead of an intention.
package unbounded

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
)

// A Hit is one place worth reading.
type Hit struct {
	Repo string // whatever the caller called this tree
	File string // path within it
	Line int
	What string
}

// A Report is what one tree came to.
type Report struct {
	// HasZip and HasImage say whether the tree does either thing at all. They
	// are the denominators: "no hits" means something different in a tree that
	// never opens a ZIP from one that opens twenty.
	HasZip   bool
	HasImage bool
	Hits     []Hit

	// Unread are the Go files this could not open or could not parse.
	//
	// ⛔ They are not hits and they are not clean. A file that could not be
	// read is a file nothing has been established about, and swallowing it is
	// the exact failure this package exists to find one level up: a sweep that
	// says nothing about what it skipped reads like a sweep that found
	// nothing.
	Unread []string
}

// ImageDecoders are the packages whose Decode allocates a pixel buffer from a
// header the input chose.
//
// ⛔ Written out rather than matched by prefix, so that what is NOT covered is
// visible. A first draft looked only for image.Decode and would have walked
// past every png.Decode in the fleet — which allocates exactly the same way. A
// decoder from outside this list, a third-party HEIC or JPEG 2000 reader, is a
// gap in this instrument rather than an absence of risk.
var ImageDecoders = map[string]bool{
	"image":                     true,
	"image/png":                 true,
	"image/jpeg":                true,
	"image/gif":                 true,
	"golang.org/x/image/bmp":    true,
	"golang.org/x/image/tiff":   true,
	"golang.org/x/image/webp":   true,
	"golang.org/x/image/vp8":    true,
	"golang.org/x/image/vp8l":   true,
	"golang.org/x/image/ccitt":  true,
	"github.com/go-images/png":  true,
	"github.com/go-images/jpeg": true,
	"github.com/go-images/gif":  true,
}

// Scan reads every non-test Go file in src and reports the two shapes.
//
// ⛔ It parses rather than greps. "io.ReadAll(" in a line of text says nothing
// about WHAT is being read, and whether the thing on the other end is chosen by
// an attacker is the entire question.
func Scan(name string, src fs.FS) Report {
	var rep Report
	fset := token.NewFileSet()

	fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") ||
			strings.HasSuffix(p, "_test.go") {
			return nil //nolint:nilerr // an unreadable entry is not a finding
		}
		b, err := fs.ReadFile(src, p)
		if err != nil {
			rep.Unread = append(rep.Unread, p)
			return nil
		}
		f, err := parser.ParseFile(fset, p, b, 0)
		if err != nil {
			// Source this cannot parse is source it cannot judge — and that is
			// said, rather than quietly counted as sound.
			rep.Unread = append(rep.Unread, p)
			return nil
		}
		rep.Hits = append(rep.Hits, scanFile(&rep, name, p, fset, f)...)
		return nil
	})

	sort.Strings(rep.Unread)
	sort.Slice(rep.Hits, func(a, b int) bool {
		if rep.Hits[a].File != rep.Hits[b].File {
			return rep.Hits[a].File < rep.Hits[b].File
		}
		return rep.Hits[a].Line < rep.Hits[b].Line
	})
	return rep
}

func scanFile(rep *Report, name, path string, fset *token.FileSet, f *ast.File) []Hit {
	zipHere, imageHere := false, false
	decoders := map[string]bool{}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		local := p[strings.LastIndexByte(p, '/')+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		switch {
		case p == "archive/zip":
			zipHere, rep.HasZip = true, true
		case ImageDecoders[p]:
			imageHere, rep.HasImage = true, true
			decoders[local] = true
		}
	}
	if !zipHere && !imageHere {
		return nil
	}

	// A file that already consults DecodeConfig is doing the thing whose
	// absence this looks for, so it is not a lead.
	consultsConfig := false
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "DecodeConfig" {
			consultsConfig = true
		}
		return true
	})

	var hits []Hit
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		line := fset.Position(call.Pos()).Line
		switch {
		case zipHere && pkg.Name == "io" && sel.Sel.Name == "ReadAll" && !limited(call):
			hits = append(hits, Hit{name, path, line,
				"io.ReadAll with no LimitReader, in a file that opens a ZIP"})
		case imageHere && decoders[pkg.Name] && sel.Sel.Name == "Decode" && !consultsConfig:
			hits = append(hits, Hit{name, path, line,
				pkg.Name + ".Decode and nothing in this file asks DecodeConfig first"})
		}
		return true
	})
	return hits
}

// limited says whether a ReadAll is handed a LimitReader — the one shape that
// makes it safe with no other context at all.
func limited(call *ast.CallExpr) bool {
	if len(call.Args) != 1 {
		return false
	}
	inner, ok := call.Args[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := inner.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "LimitReader"
}
