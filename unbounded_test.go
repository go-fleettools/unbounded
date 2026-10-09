// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package unbounded

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func src(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}

func what(hits []Hit) string {
	var b strings.Builder
	for _, h := range hits {
		b.WriteString(h.File + ":" + itoa(h.Line) + " " + h.What + "\n")
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// This is go-odf/odf at v0.5.0, cut down to the shape that mattered: an .ods of
// 67 206 bytes inflated to 64 MiB and held 222.9 MiB through this function.
const theZipDefect = `package odf

import (
	"archive/zip"
	"io"
)

func slurp(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
`

// And the same file after go-odf/odf v0.6.0.
const theZipFix = `package odf

import (
	"archive/zip"
	"io"
)

func slurpWithin(f *zip.File, limit uint64) ([]byte, error) {
	if f.UncompressedSize64 > limit {
		return nil, errTooLarge{}
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, int64(limit)+1))
}
`

// go-pdfkit/convert at v0.1.0: a 75-byte PNG claiming 10000x10000 allocated
// 381.6 MiB here, and the ceiling the caller passed changed nothing.
const theImageDefect = `package convert

import (
	"image"
	"io"
)

func Decode(r io.Reader) (image.Image, string, error) {
	m, format, err := image.Decode(r)
	if err != nil {
		return nil, "", err
	}
	return m, format, nil
}
`

const theImageFix = `package convert

import (
	"bytes"
	"image"
	"io"
)

func decodeWithin(r io.Reader, maxPixels int) (image.Image, string, error) {
	b, _ := io.ReadAll(io.LimitReader(r, 1<<28))
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, "", err
	}
	if int64(cfg.Width)*int64(cfg.Height) > int64(maxPixels) {
		return nil, "", errTooBig
	}
	m, _, err := image.Decode(bytes.NewReader(b))
	return m, format, err
}
`

func TestItFindsTheTwoDefectsItWasWrittenFor(t *testing.T) {
	// ⛔ A POSITIVE CONTROL, and it is not optional. A sweep that reports
	// nothing is indistinguishable from a sweep that could not read, and the
	// only way to tell them apart is to point it at the versions that HAD the
	// defects and watch it say so. These are those versions.
	rep := Scan("odf-v0.5.0", src(map[string]string{"parse.go": theZipDefect}))
	if !rep.HasZip {
		t.Error("a file importing archive/zip was not counted as opening one")
	}
	if len(rep.Hits) != 1 || !strings.Contains(rep.Hits[0].What, "LimitReader") {
		t.Errorf("go-odf/odf v0.5.0:\n%s", what(rep.Hits))
	}

	rep = Scan("convert-v0.1.0", src(map[string]string{"codec.go": theImageDefect}))
	if !rep.HasImage {
		t.Error("a file importing image was not counted as decoding one")
	}
	if len(rep.Hits) != 1 || !strings.Contains(rep.Hits[0].What, "DecodeConfig") {
		t.Errorf("go-pdfkit/convert v0.1.0:\n%s", what(rep.Hits))
	}
}

func TestItFindsNothingInTheVersionsThatFixedThem(t *testing.T) {
	// The negative control. Without it the positive one is satisfied by a tool
	// that flags everything.
	for name, code := range map[string]string{
		"odf-v0.6.0":     theZipFix,
		"convert-v0.2.0": theImageFix,
	} {
		rep := Scan(name, src(map[string]string{"x.go": code}))
		if len(rep.Hits) != 0 {
			t.Errorf("%s is the FIXED version and came back with:\n%s", name, what(rep.Hits))
		}
	}
}

func TestADecoderOtherThanImageDotDecodeIsFound(t *testing.T) {
	// ⛔ The blind spot a first draft had. Looking only for image.Decode would
	// have walked past every png.Decode in the fleet, which allocates from a
	// header in exactly the same way. go-pdfkit/pdfkit's DrawPNG is this shape.
	rep := Scan("pdfkit", src(map[string]string{"image.go": `package pdfkit

import (
	"bytes"
	"image/png"
)

func DrawPNG(data []byte) {
	img, _ := png.Decode(bytes.NewReader(data))
	_ = img
}
`}))
	if len(rep.Hits) != 1 || !strings.Contains(rep.Hits[0].What, "png.Decode") {
		t.Errorf("png.Decode was not found:\n%s", what(rep.Hits))
	}
}

func TestAnImportUnderAnAliasIsStillFound(t *testing.T) {
	// The package's local name is what appears at the call site, so an alias
	// has to be followed or the call is invisible.
	rep := Scan("aliased", src(map[string]string{"x.go": `package x

import (
	"bytes"
	gopng "image/png"
)

func f(b []byte) { _, _ = gopng.Decode(bytes.NewReader(b)) }
`}))
	if len(rep.Hits) != 1 {
		t.Errorf("an aliased import hid the call:\n%s", what(rep.Hits))
	}
}

func TestAFileThatNeitherOpensAZipNorDecodesAnImageIsNotRead(t *testing.T) {
	rep := Scan("plain", src(map[string]string{"x.go": `package x

import "io"

func f(r io.Reader) { _, _ = io.ReadAll(r) }
`}))
	if rep.HasZip || rep.HasImage || len(rep.Hits) != 0 {
		t.Errorf("an ordinary io.ReadAll was flagged:\n%s", what(rep.Hits))
	}
}

func TestTestFilesAreNotScanned(t *testing.T) {
	// ⛔ A test deliberately builds hostile input, so flagging what a _test.go
	// does would bury every real lead under its own test suite. go-odf/odf's
	// limits_test.go does exactly this.
	rep := Scan("x", src(map[string]string{"x_test.go": theImageDefect}))
	if len(rep.Hits) != 0 || rep.HasImage {
		t.Errorf("a test file was scanned:\n%s", what(rep.Hits))
	}
}

func TestSourceThatWillNotParseIsNamedRatherThanPassedOver(t *testing.T) {
	// ⛔ Source this cannot parse is source it cannot judge. Reporting it as a
	// hit would be wrong; reporting it as CLEAN would be the same failure this
	// package exists to find one level up — a sweep that says nothing about
	// what it skipped reads like one that found nothing.
	rep := Scan("broken", src(map[string]string{
		"bad.go":  "package x\nthis is not Go at all {{{",
		"good.go": theImageDefect,
	}))
	if len(rep.Hits) != 1 {
		t.Errorf("a file that does not parse changed the hits:\n%s", what(rep.Hits))
	}
	if len(rep.Unread) != 1 || rep.Unread[0] != "bad.go" {
		t.Errorf("the unparseable file was passed over in silence: %v", rep.Unread)
	}
}

// listsButWillNotOpen names a .go file in its listing and refuses to open it,
// which is what a permission problem or a broken symlink looks like from here.
type listsButWillNotOpen struct{ fs.FS }

func (l listsButWillNotOpen) Open(name string) (fs.File, error) {
	if strings.HasSuffix(name, "secret.go") {
		return nil, fs.ErrPermission
	}
	return l.FS.Open(name)
}

func TestAFileThatListsAndWillNotOpenIsNamed(t *testing.T) {
	rep := Scan("x", listsButWillNotOpen{src(map[string]string{
		"secret.go": theImageDefect,
		"open.go":   "package x\n",
	})})
	if len(rep.Hits) != 0 {
		t.Errorf("hits from a file that could not be opened: %s", what(rep.Hits))
	}
	if len(rep.Unread) != 1 || rep.Unread[0] != "secret.go" {
		t.Errorf("an unreadable file was reported as clean: %v", rep.Unread)
	}
}

func TestHitsComeBackInFileAndLineOrder(t *testing.T) {
	rep := Scan("x", src(map[string]string{
		"b.go": theImageDefect,
		"a.go": theZipDefect,
	}))
	if len(rep.Hits) != 2 {
		t.Fatalf("%d hits", len(rep.Hits))
	}
	if rep.Hits[0].File != "a.go" || rep.Hits[1].File != "b.go" {
		t.Errorf("out of order: %s then %s", rep.Hits[0].File, rep.Hits[1].File)
	}
	if rep.Hits[0].Repo != "x" {
		t.Errorf("the tree's name did not travel: %q", rep.Hits[0].Repo)
	}

	// Two in ONE file, so the line comparison decides rather than the name.
	rep = Scan("x", src(map[string]string{"one.go": `package x

import (
	"bytes"
	"image"
	"image/png"
)

func a(b []byte) { _, _, _ = image.Decode(bytes.NewReader(b)) }
func z(b []byte) { _, _ = png.Decode(bytes.NewReader(b)) }
`}))
	if len(rep.Hits) != 2 {
		t.Fatalf("%d hits in one file:\n%s", len(rep.Hits), what(rep.Hits))
	}
	if rep.Hits[0].Line >= rep.Hits[1].Line {
		t.Errorf("two hits in one file came back as lines %d then %d",
			rep.Hits[0].Line, rep.Hits[1].Line)
	}
}

func TestDirectoriesAndNonGoFilesAreWalkedPast(t *testing.T) {
	// ⛔ A tree is not a flat list of .go files. A directory, a README and a
	// vendored testdata fixture all arrive at the same callback, and the walk
	// has to step over each without counting it or losing the files after it.
	rep := Scan("x", src(map[string]string{
		"README.md":               "not Go",
		"internal/deep/codec.go":  theImageDefect,
		"internal/deep/notes.txt": "nor this",
		"testdata/sample.bin":     "\x00\x01",
		"internal/deep/x_test.go": theZipDefect,
		"internal/other/plain.go": "package other\n",
	}))
	if len(rep.Hits) != 1 {
		t.Fatalf("expected the one real file to be found:\n%s", what(rep.Hits))
	}
	if rep.Hits[0].File != "internal/deep/codec.go" {
		t.Errorf("found %q", rep.Hits[0].File)
	}
}

func TestAReadAllOfSomethingOtherThanOneArgumentIsNotMistaken(t *testing.T) {
	// limited() looks at the single argument, and a call with none would
	// otherwise index past it.
	rep := Scan("x", src(map[string]string{"x.go": `package x

import (
	"archive/zip"
	"io"
)

var _ = zip.Store

func f() { _, _ = io.ReadAll() }
`}))
	if len(rep.Hits) != 1 {
		t.Errorf("a malformed ReadAll was not handled:\n%s", what(rep.Hits))
	}
}

func TestACallOnSomethingThatIsNotAPackageIsIgnored(t *testing.T) {
	// r.Decode() is a method on a value, not a package's function, and the
	// selector's left-hand side is not an identifier naming an import.
	rep := Scan("x", src(map[string]string{"x.go": `package x

import "image"

type dec struct{}

func (dec) Decode() {}

func f(d dec, s struct{ x image.Point }) {
	d.Decode()
	_ = s.x.String()
}
`}))
	if len(rep.Hits) != 0 {
		t.Errorf("a method call was taken for a decoder:\n%s", what(rep.Hits))
	}
}
