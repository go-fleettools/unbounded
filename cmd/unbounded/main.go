// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

// unbounded sweeps organisations, or directories, for two shapes: a ZIP entry
// read whole with no ceiling, and an image decoded before anything has looked
// at its header.
//
//	unbounded go-odf go-pdfkit go-gfx
//	unbounded ./some/checkout          # a directory is scanned in place
//
// Both shapes are how a few hundred bytes of input become a few gigabytes of
// memory. Both are also perfectly correct over input the program produced
// itself, so every hit is a LEAD, not a verdict.
package main

import "os"

// osExit is a variable so the tests can reach the exit path without ending the
// test binary.
var osExit = os.Exit

func main() { osExit(run(os.Args[1:], os.Stdout, os.Stderr)) }
