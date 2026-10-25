module github.com/go-fleettools/unbounded

// ⛔ The PATCH, because govulncheck judges the standard library by the version
// this line claims, and nothing else here states one. GO-2026-6809 — crypto/x509,
// where compromised intermediate certificates are no longer refused by
// Certificate.Verify — has its fix RELEASED in 1.27.3, and vulngate accepts no
// waiver for an advisory whose fix is released. The lane runs this very line,
// through `go-version-file: go.mod`.
//
// ⚠ Unlike its sibling tools, this raise CROSSES MINORS — 1.25 to 1.27 — so a
// consumer on 1.25 or 1.26 that could install this before cannot now. For a
// module that ships only a command that is the intended effect: `go install`
// makes it the main module, and its own floor is the one honoured.
go 1.27.3
