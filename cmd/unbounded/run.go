// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/go-fleettools/unbounded"
)

// listRepos and clone are variables so the tests can sweep a fleet that does
// not exist, without a network and without a GitHub token.
var (
	listRepos = func(org string) ([]byte, error) {
		return exec.Command("gh", "api", "orgs/"+org+"/repos?per_page=100").Output()
	}
	clone = func(full, dir string) error {
		return exec.Command("git", "clone", "-q", "--depth", "1",
			"https://github.com/"+full+".git", dir).Run()
	}
	// mkTemp is where the clones go. A full disk is the ordinary way this
	// fails, and a sweep that cannot write must say so rather than report an
	// empty fleet.
	mkTemp = func() (string, error) { return os.MkdirTemp("", "unbounded") }
)

type repo struct {
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
}

func run(args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errw, "usage: unbounded <organisation|directory>...")
		return 2
	}

	tmp, err := mkTemp()
	if err != nil {
		fmt.Fprintln(errw, "unbounded:", err)
		return 1
	}
	defer os.RemoveAll(tmp)

	var hits []unbounded.Hit
	var unreadable []string
	var read, unread, withZip, withImage int

	for _, arg := range args {
		// ⛔ A directory is scanned in place, and that is not a convenience:
		// it is how this tool gets a POSITIVE CONTROL. A sweep reporting
		// nothing is indistinguishable from one that could not read, and the
		// way to tell them apart is to point it at a checkout of the version
		// that HAD the defect and watch it say so.
		if fi, err := os.Stat(arg); err == nil && fi.IsDir() {
			rep := unbounded.Scan(filepath.Base(arg), os.DirFS(arg))
			read++
			count(&withZip, &withImage, rep)
			hits = append(hits, rep.Hits...)
			unreadable = append(unreadable, prefix(filepath.Base(arg), rep.Unread)...)
			continue
		}

		listing, err := listRepos(arg)
		if err != nil {
			fmt.Fprintf(errw, "⛔ %s: NOT LISTED (%v)\n", arg, err)
			unread++
			continue
		}
		var rs []repo
		if err := json.Unmarshal(listing, &rs); err != nil {
			fmt.Fprintf(errw, "⛔ %s: unreadable listing\n", arg)
			unread++
			continue
		}
		for _, r := range rs {
			if r.Archived {
				continue
			}
			full := arg + "/" + r.Name
			dir := filepath.Join(tmp, arg+"-"+r.Name)
			if err := clone(full, dir); err != nil {
				fmt.Fprintf(errw, "⛔ %-34s NOT CLONED\n", full)
				unread++
				continue
			}
			rep := unbounded.Scan(full, os.DirFS(dir))
			read++
			count(&withZip, &withImage, rep)
			hits = append(hits, rep.Hits...)
			unreadable = append(unreadable, prefix(full, rep.Unread)...)
		}
	}

	sort.Slice(hits, func(a, b int) bool {
		if hits[a].Repo != hits[b].Repo {
			return hits[a].Repo < hits[b].Repo
		}
		return hits[a].File < hits[b].File
	})
	for _, h := range hits {
		fmt.Fprintf(out, "  %-30s %s:%d  %s\n", h.Repo, h.File, h.Line, h.What)
	}

	// ⛔ Both halves, out loud, every time. "No hits" means something different
	// in a fleet where nothing opens a ZIP from one where twenty do, and a
	// sweep that could not read half the repositories must not read as a clean
	// one.
	fmt.Fprintf(out, "\n%d repositories read, %d NOT read\n", read, unread)
	fmt.Fprintf(out, "%d open a ZIP, %d decode an image, %d lead(s) to look at\n",
		withZip, withImage, len(hits))
	if len(unreadable) > 0 {
		// ⛔ Files that listed and could not be read or parsed. They are
		// neither hits nor clean, and leaving them out of the report is the
		// same mistake this tool looks for: a number that quietly excludes
		// what it could not see.
		sort.Strings(unreadable)
		fmt.Fprintf(out, "%d Go file(s) could NOT be read or parsed, so nothing is claimed "+
			"about them:\n", len(unreadable))
		for _, u := range unreadable {
			fmt.Fprintf(out, "  %s\n", u)
		}
	}
	if len(hits) > 0 {
		fmt.Fprintln(out, "\nEvery hit is a LEAD. Both shapes are correct over input the "+
			"program produced itself; read each one and ask where the bytes come from.")
	}
	if read == 0 {
		fmt.Fprintln(errw, "NOTHING was read — that is a failure, not a clean result")
		return 1
	}
	return 0
}

// prefix qualifies a tree's file paths with the tree's own name, so the lines
// mean something once several trees are in one report.
func prefix(tree string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, tree+"/"+p)
	}
	return out
}

func count(zip, img *int, rep unbounded.Report) {
	if rep.HasZip {
		*zip++
	}
	if rep.HasImage {
		*img++
	}
}
