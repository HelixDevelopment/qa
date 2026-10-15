// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llmctl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvBin is the environment variable that points at an llmctl binary.
const EnvBin = "LLMCTL_BIN"

// Source says where a detected llmctl binary came from.
type Source string

const (
	// SourceEnv: the path named by LLMCTL_BIN.
	SourceEnv Source = "env"
	// SourcePath: found on PATH as "llmctl".
	SourcePath Source = "path"
	// SourceVendored: the vendored submodule checkout.
	SourceVendored Source = "vendored"
)

// ErrNotFound is returned (wrapped) by Detect when no llmctl binary
// could be located by any of the three strategies.
var ErrNotFound = errors.New("llmctl: binary not found")

// DetectOptions parameterises Detect. The zero value uses the real
// process environment and no vendored fallback.
type DetectOptions struct {
	// Getenv replaces os.Getenv (tests).
	Getenv func(string) string
	// VendoredDir is the root of a vendored llmctl checkout; the
	// binary is expected at <VendoredDir>/bin/llmctl.
	VendoredDir string
}

// Detection is the result of Detect.
type Detection struct {
	Bin    string
	Source Source
	// Tried lists every candidate that was examined, in order, with
	// the reason it was rejected (for diagnostics; never secrets).
	Tried []string
}

// Detect resolves the llmctl binary in the mandated order:
// LLMCTL_BIN, then PATH, then the vendored checkout. An LLMCTL_BIN
// that is set but unusable is an error, not a silent fall-through:
// an explicit operator choice must never be ignored.
func Detect(opts DetectOptions) (Detection, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	var d Detection

	if v := strings.TrimSpace(getenv(EnvBin)); v != "" {
		if reason := notExecutable(v); reason != "" {
			d.Tried = append(d.Tried, fmt.Sprintf("%s=%s: %s", EnvBin, v, reason))
			return d, fmt.Errorf("%w: %s is set but unusable (%s)", ErrNotFound, EnvBin, reason)
		}
		d.Bin, d.Source = v, SourceEnv
		d.Tried = append(d.Tried, fmt.Sprintf("%s=%s: ok", EnvBin, v))
		return d, nil
	}
	d.Tried = append(d.Tried, EnvBin+": unset")

	for _, dir := range filepath.SplitList(getenv("PATH")) {
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, "llmctl")
		if notExecutable(cand) == "" {
			d.Bin, d.Source = cand, SourcePath
			d.Tried = append(d.Tried, "PATH: "+cand)
			return d, nil
		}
	}
	d.Tried = append(d.Tried, "PATH: no llmctl")

	if opts.VendoredDir != "" {
		cand := filepath.Join(opts.VendoredDir, "bin", "llmctl")
		if reason := notExecutable(cand); reason == "" {
			d.Bin, d.Source = cand, SourceVendored
			d.Tried = append(d.Tried, "vendored: "+cand)
			return d, nil
		} else {
			d.Tried = append(d.Tried, fmt.Sprintf("vendored %s: %s", cand, reason))
		}
	} else {
		d.Tried = append(d.Tried, "vendored: no directory configured")
	}
	return d, fmt.Errorf("%w (tried: %s)", ErrNotFound, strings.Join(d.Tried, "; "))
}

// SetupArgv returns the argv that bootstraps llmctl for a detection
// that fell back to the vendored checkout (`llmctl setup`: doctor,
// build engines, print plan). It is returned, never executed, by this
// package: running it is an explicit operator/script decision.
func SetupArgv(d Detection) []string {
	if d.Bin == "" {
		return nil
	}
	return []string{d.Bin, "setup"}
}

// notExecutable returns "" when path is a regular, executable file,
// otherwise a short reason.
func notExecutable(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "does not exist"
		}
		return "stat failed: " + err.Error()
	}
	if fi.IsDir() {
		return "is a directory"
	}
	if fi.Mode().Perm()&0o111 == 0 {
		return "not executable"
	}
	return ""
}
