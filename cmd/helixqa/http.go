// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"digital.vasic.helixqa/pkg/autonomous"
	"digital.vasic.helixqa/pkg/config"
	"digital.vasic.helixqa/pkg/testbank"
)

// cmdHTTP runs a test bank's `http:`-action cases against a live
// HTTP server WITHOUT a browser (Playwright) or an LLM (autonomous
// mode). It is the standalone bridge between the structured
// ActionTypeHTTP bank schema and the existing, LLM-free
// autonomous.HTTPExecutor: it loads one or more banks, selects every
// step whose action parses as ActionTypeHTTP, runs each through the
// executor against the operator-supplied base URL, and reports a
// per-case PASS/FAIL with the real status code and body excerpt.
//
// Exit code is 0 only when every executed HTTP step PASSed; any FAIL
// (or any executor error) yields a non-zero exit so CI / release
// gates can depend on it. Cases with no http: steps are reported as
// SKIPPED (no http: steps) and do NOT affect the exit code.
//
// Decoupling (CONST-051(B)): the base URL is a required flag — never
// hardcoded — so this command is reusable against any HTTP server a
// bank targets, not just one project.
//
// Closes the gap where the only CLI run paths were `run -platform
// web` (needs Playwright) and `autonomous` (needs an Ollama LLM):
// neither could drive the existing 16-case auth bank's
// http: cases against a live server. The HTTPExecutor existed; this
// is the missing CLI bridge.
func cmdHTTP(args []string) {
	fs := flag.NewFlagSet("http", flag.ExitOnError)
	bank := fs.String("bank", "",
		"Path to a single test bank YAML file (alias for -banks)")
	banks := fs.String("banks", "",
		"Comma-separated test bank paths (files or directories)")
	baseURL := fs.String("base-url", "",
		"Base URL of the live HTTP server (e.g. http://127.0.0.1:8080)")
	adminUser := fs.String("admin-user", "",
		"Username for auth:admin steps (no built-in default; prefer -admin-user-env)")
	adminPass := fs.String("admin-pass", "",
		"Password for auth:admin steps (no built-in default; prefer -admin-pass-env)")
	adminUserEnv := fs.String("admin-user-env", "",
		"NAME of the environment variable holding the auth:admin username")
	adminPassEnv := fs.String("admin-pass-env", "",
		"NAME of the environment variable holding the auth:admin password")
	loginPath := fs.String("login-path", "",
		"Override the login endpoint (default /api/v1/auth/login)")
	tokenField := fs.String("token-field", "",
		"Override the login-response token JSON field. Supports dotted\n    \tnested paths (e.g. session.session_token). Default: try\n    \tsession_token, then token, then access_token.")
	timeout := fs.Duration("timeout", 30*time.Second,
		"Per-request HTTP timeout")
	verbose := fs.Bool("verbose", false,
		"Print every step result, not just per-case summaries")
	jsonOut := fs.Bool("json", false,
		"Emit a machine-readable JSON summary instead of text")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	// Merge -bank and -banks into one bank-spec string.
	bankSpec := *banks
	if *bank != "" {
		if bankSpec != "" {
			bankSpec += "," + *bank
		} else {
			bankSpec = *bank
		}
	}
	if bankSpec == "" {
		fmt.Fprintln(os.Stderr, "error: --bank (or --banks) is required")
		fs.Usage()
		os.Exit(1)
	}
	if *baseURL == "" {
		fmt.Fprintln(os.Stderr,
			"error: --base-url is required (the live server to drive); it is never hardcoded")
		fs.Usage()
		os.Exit(1)
	}

	// Load banks via the same manager the other subcommands use, so
	// the schema parsing is identical.
	mgr := testbank.NewManager()
	for _, path := range config.ParseBanks(bankSpec) {
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if info.IsDir() {
			declined, err := mgr.LoadDirVerbose(path)
			// HXC-305: a directory scan must never discard a file
			// without saying so — report every declined bank twin
			// explicitly, not just via the package logger. Reported
			// BEFORE the error check, because LoadDirVerbose returns a
			// populated declined list ALONGSIDE an error on every
			// abort path that could have declined anything (a
			// duplicate id, a failing id floor, an unparseable or
			// emptied bank), and exiting on the error first threw that
			// list away on exactly the paths where it is most
			// diagnostic: the operator saw "duplicate test case ID"
			// with no indication of which twin files had already been
			// declined, or what content they took with them.
			for _, d := range declined {
				fmt.Fprintf(os.Stderr, "declined %s: %s\n", d.Path, d.Reason)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		} else if err := mgr.LoadFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	exec := autonomous.NewHTTPExecutor(*baseURL)
	exec.HTTPClient.Timeout = *timeout
	au, ap := *adminUser, *adminPass
	if au == "" && *adminUserEnv != "" {
		au = os.Getenv(*adminUserEnv)
	}
	if ap == "" && *adminPassEnv != "" {
		ap = os.Getenv(*adminPassEnv)
	}
	if au != "" || ap != "" {
		exec.AdminCreds = autonomous.Credentials{
			Username: au,
			Password: ap,
		}
	}
	if *loginPath != "" {
		exec.LoginPath = *loginPath
	}
	if *tokenField != "" {
		exec.TokenField = *tokenField
	}

	ctx := context.Background()
	report := RunHTTPBank(ctx, exec, mgr.All(), *verbose, os.Stdout)

	if *jsonOut {
		emitHTTPJSON(report, os.Stdout)
	}

	if report.FailedCases > 0 || report.ErroredCases > 0 {
		os.Exit(1)
	}
}

// HTTPCaseResult is the per-case outcome of an http-bank run.
type HTTPCaseResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Verdict string `json:"verdict"` // PASS | FAIL | SKIP
	Steps   int    `json:"http_steps"`
	Passed  int    `json:"passed_steps"`
	Failed  int    `json:"failed_steps"`
	Skipped int    `json:"skipped_steps"`
	Detail  string `json:"detail"` // first failing/last step message
}

// HTTPRunReport aggregates the per-case results of an http-bank run.
type HTTPRunReport struct {
	BaseURL      string           `json:"base_url"`
	TotalCases   int              `json:"total_cases"`
	PassedCases  int              `json:"passed_cases"`
	FailedCases  int              `json:"failed_cases"`
	SkippedCases int              `json:"skipped_cases"`
	ErroredCases int              `json:"errored_cases"`
	Cases        []HTTPCaseResult `json:"cases"`
}

// RunHTTPBank executes every ActionTypeHTTP step in the given cases
// through exec, writes per-case PASS/FAIL lines to w, and returns an
// aggregate report. It is extracted from cmdHTTP so unit tests can
// drive it against an httptest server with no process/flag plumbing.
//
// A case PASSes when every one of its http: steps PASSes; it FAILs on
// the first http: step that fails or errors; it is SKIPPED when it has
// no http: steps at all (non-HTTP banks/cases are not this command's
// concern and must not pollute the exit code).
func RunHTTPBank(
	ctx context.Context,
	exec *autonomous.HTTPExecutor,
	cases []*testbank.TestCase,
	verbose bool,
	w writer,
) *HTTPRunReport {
	rep := &HTTPRunReport{BaseURL: exec.BaseURL}

	for _, tc := range cases {
		cr := HTTPCaseResult{ID: tc.ID, Name: tc.Name}

		for _, step := range tc.Steps {
			at, value := step.ParseAction()
			if at != testbank.ActionTypeHTTP {
				continue
			}
			cr.Steps++
			method, path := parseMethodPath(value)
			res := exec.Execute(ctx, method, path, step)
			switch {
			case res.Skipped:
				cr.Skipped++
				if verbose {
					fmt.Fprintf(w, "    [skip] %s/%s: %s\n",
						tc.ID, step.Name, res.Message)
				}
			case res.Success:
				cr.Passed++
				if verbose {
					fmt.Fprintf(w, "    [ok]   %s/%s: %s\n",
						tc.ID, step.Name, res.Message)
				}
			default:
				cr.Failed++
				if cr.Detail == "" {
					cr.Detail = res.Message
				}
				if verbose {
					fmt.Fprintf(w, "    [FAIL] %s/%s: %s\n",
						tc.ID, step.Name, res.Message)
				}
			}
		}

		// Classify the case.
		switch {
		case cr.Steps == 0:
			cr.Verdict = "SKIP"
			rep.SkippedCases++
			fmt.Fprintf(w, "SKIP  %-14s %s (no http: steps)\n", cr.ID, tc.Name)
		case cr.Failed > 0:
			cr.Verdict = "FAIL"
			rep.FailedCases++
			fmt.Fprintf(w, "FAIL  %-14s %s — %d/%d http steps failed: %s\n",
				cr.ID, tc.Name, cr.Failed, cr.Steps, cr.Detail)
		case cr.Passed == 0 && cr.Skipped > 0:
			// All http: steps explicitly skipped — honest SKIP, not PASS.
			cr.Verdict = "SKIP"
			rep.SkippedCases++
			fmt.Fprintf(w, "SKIP  %-14s %s (%d http steps skipped)\n",
				cr.ID, tc.Name, cr.Skipped)
		default:
			cr.Verdict = "PASS"
			rep.PassedCases++
			fmt.Fprintf(w, "PASS  %-14s %s (%d/%d http steps)\n",
				cr.ID, tc.Name, cr.Passed, cr.Steps)
		}
		rep.Cases = append(rep.Cases, cr)
		rep.TotalCases++
	}

	fmt.Fprintf(w,
		"\nHTTP bank summary: %d cases — %d PASS, %d FAIL, %d SKIP  (base-url %s)\n",
		rep.TotalCases, rep.PassedCases, rep.FailedCases,
		rep.SkippedCases, rep.BaseURL)

	return rep
}

// writer is the minimal output sink RunHTTPBank needs — satisfied by
// *os.File, *bytes.Buffer, etc. Kept local so the function is unit-
// testable without importing io at every call site.
type writer interface {
	Write(p []byte) (int, error)
}

// parseMethodPath splits a parsed http action value ("METHOD /path")
// into method + path, defaulting the method to GET when only a path
// is present. Mirrors the executor's own tolerant parsing so a bank
// author can write "http: /health" as a shorthand GET.
func parseMethodPath(value string) (method, path string) {
	fields := strings.Fields(value)
	switch len(fields) {
	case 0:
		return "", ""
	case 1:
		return "GET", fields[0]
	default:
		return fields[0], fields[1]
	}
}

// emitHTTPJSON writes the report as indented JSON to w.
func emitHTTPJSON(rep *HTTPRunReport, w writer) {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(w, "error: marshal json report: %v\n", err)
		return
	}
	fmt.Fprintln(w, string(data))
}
