// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const theImageDefect = `package convert

import (
	"image"
	"io"
)

func Decode(r io.Reader) (image.Image, string, error) {
	m, format, err := image.Decode(r)
	return m, format, err
}
`

// tree writes a checkout on disk, which is what the directory path reads.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fleet replaces the two things that reach the network, so a sweep can be run
// over a world that does not exist.
func fleet(t *testing.T, repos map[string]map[string]string, broken ...string) {
	t.Helper()
	wasList, wasClone := listRepos, clone
	t.Cleanup(func() { listRepos, clone = wasList, wasClone })

	listRepos = func(org string) ([]byte, error) {
		for _, b := range broken {
			if b == org {
				return nil, errors.New("404")
			}
		}
		var names []string
		for full := range repos {
			if o, n, ok := strings.Cut(full, "/"); ok && o == org {
				names = append(names, `{"name":"`+n+`","archived":false}`)
			}
		}
		return []byte("[" + strings.Join(names, ",") + "]"), nil
	}
	clone = func(full, dir string) error {
		files, ok := repos[full]
		if !ok {
			return errors.New("no such repository")
		}
		if files == nil {
			return errors.New("clone refused")
		}
		for name, body := range files {
			p := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestADirectoryIsScannedInPlace(t *testing.T) {
	// ⛔ This is the positive control the tool exists to be able to run: point
	// it at a checkout of the version that HAD the defect and watch it say so.
	dir := tree(t, map[string]string{"codec.go": theImageDefect})
	var out, errw bytes.Buffer
	if code := run([]string{dir}, &out, &errw); code != 0 {
		t.Fatalf("exited %d: %s", code, errw.String())
	}
	s := out.String()
	if !strings.Contains(s, "DecodeConfig") {
		t.Errorf("the defect was not reported:\n%s", s)
	}
	if !strings.Contains(s, "1 repositories read, 0 NOT read") {
		t.Errorf("the denominators are wrong:\n%s", s)
	}
	if !strings.Contains(s, "LEAD") {
		t.Error("a hit was reported without saying it is a lead rather than a verdict")
	}
}

func TestAnOrganisationIsListedAndCloned(t *testing.T) {
	fleet(t, map[string]map[string]string{
		"go-x/bad":  {"a.go": theImageDefect},
		"go-x/fine": {"b.go": "package b\n"},
	})
	var out, errw bytes.Buffer
	if code := run([]string{"go-x"}, &out, &errw); code != 0 {
		t.Fatalf("exited %d: %s", code, errw.String())
	}
	s := out.String()
	if !strings.Contains(s, "go-x/bad") || strings.Contains(s, "go-x/fine a.go") {
		t.Errorf("the wrong repository was named:\n%s", s)
	}
	if !strings.Contains(s, "2 repositories read") {
		t.Errorf("both repositories should be counted:\n%s", s)
	}
}

func TestWhatCouldNotBeReadIsSaidOutLoud(t *testing.T) {
	// ⛔ A sweep that could not read half the fleet must not come back looking
	// like a clean one. The NOT-read count is the whole difference.
	fleet(t, map[string]map[string]string{
		"go-y/ok":      {"a.go": "package a\n"},
		"go-y/refuses": nil, // clone fails
	}, "go-z") // the organisation itself will not list

	var out, errw bytes.Buffer
	if code := run([]string{"go-y", "go-z"}, &out, &errw); code != 0 {
		t.Fatalf("exited %d", code)
	}
	if !strings.Contains(out.String(), "1 repositories read, 2 NOT read") {
		t.Errorf("the denominators do not account for both failures:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), "NOT CLONED") || !strings.Contains(errw.String(), "NOT LISTED") {
		t.Errorf("the two failures do not read differently:\n%s", errw.String())
	}
}

func TestAListingThatIsNotJSONIsReportedRatherThanParsedAsEmpty(t *testing.T) {
	was := listRepos
	t.Cleanup(func() { listRepos = was })
	listRepos = func(string) ([]byte, error) { return []byte("<html>who knows</html>"), nil }

	var out, errw bytes.Buffer
	run([]string{"go-q"}, &out, &errw)
	if !strings.Contains(errw.String(), "unreadable listing") {
		t.Errorf("a listing that is not JSON was taken for an empty organisation:\n%s", errw.String())
	}
}

func TestReadingNothingIsAFailureAndNotACleanResult(t *testing.T) {
	// ⛔ Zero repositories read and no leads would otherwise print exactly like
	// a healthy fleet.
	fleet(t, map[string]map[string]string{}, "go-nothing")
	var out, errw bytes.Buffer
	if code := run([]string{"go-nothing"}, &out, &errw); code == 0 {
		t.Error("a sweep that read nothing exited 0")
	}
	if !strings.Contains(errw.String(), "failure, not a clean result") {
		t.Errorf("it did not say so:\n%s", errw.String())
	}
}

func TestNoArgumentsIsAUsageError(t *testing.T) {
	var out, errw bytes.Buffer
	if code := run(nil, &out, &errw); code != 2 {
		t.Errorf("exited %d", code)
	}
	if !strings.Contains(errw.String(), "usage") {
		t.Errorf("no usage: %s", errw.String())
	}
}

func TestArchivedRepositoriesAreSkipped(t *testing.T) {
	was := listRepos
	t.Cleanup(func() { listRepos = was })
	listRepos = func(string) ([]byte, error) {
		return []byte(`[{"name":"retired","archived":true}]`), nil
	}
	var out, errw bytes.Buffer
	run([]string{"go-r"}, &out, &errw)
	if !strings.Contains(out.String(), "0 repositories read") {
		t.Errorf("an archived repository was swept:\n%s", out.String())
	}
}

func TestHitsAreSortedByRepositoryThenFile(t *testing.T) {
	fleet(t, map[string]map[string]string{
		"go-s/b": {"z.go": theImageDefect},
		"go-s/a": {"m.go": theImageDefect, "a.go": theImageDefect},
	})
	var out, errw bytes.Buffer
	run([]string{"go-s"}, &out, &errw)
	s := out.String()
	ia := strings.Index(s, "go-s/a")
	ib := strings.Index(s, "go-s/b")
	if ia < 0 || ib < 0 || ia > ib {
		t.Errorf("out of order:\n%s", s)
	}
	if am, aa := strings.Index(s, "m.go"), strings.Index(s, "a.go"); aa > am {
		t.Errorf("files out of order within a repository:\n%s", s)
	}
}

func TestNowhereToPutTheClonesIsReported(t *testing.T) {
	// ⛔ A full disk is the ordinary way this fails, and it must not come back
	// looking like a fleet with nothing in it.
	was := mkTemp
	t.Cleanup(func() { mkTemp = was })
	mkTemp = func() (string, error) { return "", errors.New("no space left on device") }

	var out, errw bytes.Buffer
	if code := run([]string{"go-x"}, &out, &errw); code != 1 {
		t.Errorf("exited %d", code)
	}
	if !strings.Contains(errw.String(), "no space") {
		t.Errorf("it said %q", errw.String())
	}
}

func TestARepositoryThatOpensAZipIsCounted(t *testing.T) {
	// ⛔ The two denominators are what make "no leads" mean anything: it says
	// something different in a fleet where nothing opens a ZIP from one where
	// several do.
	fleet(t, map[string]map[string]string{
		"go-z/reader": {"a.go": `package a

import (
	"archive/zip"
	"io"
)

func f(x *zip.File) { rc, _ := x.Open(); _, _ = io.ReadAll(io.LimitReader(rc, 1<<20)) }
`},
	})
	var out, errw bytes.Buffer
	run([]string{"go-z"}, &out, &errw)
	if !strings.Contains(out.String(), "1 open a ZIP") {
		t.Errorf("a repository that opens a ZIP was not counted:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "0 lead(s)") {
		t.Errorf("a bounded read was flagged:\n%s", out.String())
	}
}

func TestAFileThatCouldNotBeParsedIsNamedInTheReport(t *testing.T) {
	// ⛔ Neither a hit nor clean. Leaving it out of the report is the same
	// mistake this tool looks for one level up: a number that quietly excludes
	// what it could not see.
	dir := tree(t, map[string]string{
		"broken.go": "package x\nthis is not Go {{{",
		"fine.go":   "package x\n",
	})
	var out, errw bytes.Buffer
	if code := run([]string{dir}, &out, &errw); code != 0 {
		t.Fatalf("exited %d", code)
	}
	s := out.String()
	if !strings.Contains(s, "could NOT be read or parsed") || !strings.Contains(s, "broken.go") {
		t.Errorf("the unparseable file was passed over in silence:\n%s", s)
	}
}

func TestMainHandsBackTheExitCode(t *testing.T) {
	old, oldArgs := osExit, os.Args
	t.Cleanup(func() { osExit, os.Args = old, oldArgs })
	got := -1
	osExit = func(code int) { got = code }
	os.Args = []string{"unbounded"}
	main()
	if got != 2 {
		t.Errorf("main exited %d with no arguments", got)
	}
}

func TestTheRealListerAndClonerAreWhatTheySay(t *testing.T) {
	// ⛔ listRepos and clone are the one pair every other test replaces, so
	// nothing else would ever run them. This does not need a network: it needs
	// them to fail the way a missing thing fails, rather than panic or succeed.
	if _, err := listRepos("this-organisation-does-not-exist-" + t.Name()); err == nil {
		t.Error("listing an organisation that cannot exist succeeded")
	}
	if err := clone("go-fleettools/this-repository-does-not-exist", t.TempDir()); err == nil {
		t.Error("cloning a repository that cannot exist succeeded")
	}
}
