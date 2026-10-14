// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"fmt"

	"digital.vasic.helixqa/pkg/detector"
	"digital.vasic.helixqa/pkg/navigator"
)

// RealExecutorConfig holds platform-specific configuration used
// by RealExecutorFactory to create executors.
type RealExecutorConfig struct {
	// AndroidDevice is the ADB device/emulator serial.
	AndroidDevice string

	// AndroidPackage is the Android application package name.
	AndroidPackage string

	// WebURL is the URL for web platform testing.
	WebURL string

	// WebBrowser is the browser to use for web testing.
	WebBrowser string

	// DesktopProcess is the desktop process name to monitor.
	DesktopProcess string

	// DesktopDisplay is the X11 display (e.g. ":0").
	DesktopDisplay string

	// CLICommand is the CLI command for the "cli" platform.
	// Defaults to "bash" when empty.
	CLICommand string

	// APIURL is the base URL for the "api" platform executor.
	// Defaults to "http://localhost:8080" when empty.
	APIURL string
}

// RealExecutorFactory creates platform-specific ActionExecutor
// instances for Android, Android TV, web, and desktop platforms.
// Android and Android TV use ADBExecutor; web uses PlaywrightExecutor;
// desktop uses X11Executor. Executors are cached per platform to
// reuse browser sessions and avoid redundant launches.
type RealExecutorFactory struct {
	config RealExecutorConfig
	cache  map[string]navigator.ActionExecutor
	// runner runs the reachability probe's host commands (nil = os/exec).
	runner detector.CommandRunner
}

// Probe checks, once per platform (StructuredTestExecutor caches it), that
// the configured device / display / browser answers — see
// probePlatformReachable.
func (f *RealExecutorFactory) Probe(ctx context.Context, platform string) error {
	runner := f.runner
	if runner == nil {
		runner = detector.NewExecRunner()
	}
	return probePlatformReachable(ctx, platform, f.config.AndroidDevice, runner, f.Create)
}

// NewRealExecutorFactory creates a RealExecutorFactory with the
// given configuration.
func NewRealExecutorFactory(cfg RealExecutorConfig) *RealExecutorFactory {
	return &RealExecutorFactory{
		config: cfg,
		cache:  make(map[string]navigator.ActionExecutor),
	}
}

// Create returns the appropriate ActionExecutor for the platform.
// Supported platforms: "android", "androidtv", "web", "desktop".
// Executors are cached per platform to reuse browser sessions.
func (f *RealExecutorFactory) Create(
	platform string,
) (navigator.ActionExecutor, error) {
	if exec, ok := f.cache[platform]; ok {
		return exec, nil
	}
	exec, err := f.create(platform)
	if err != nil {
		return nil, err
	}
	f.cache[platform] = exec
	return exec, nil
}

func (f *RealExecutorFactory) create(
	platform string,
) (navigator.ActionExecutor, error) {
	switch platform {
	case "android", "androidtv":
		return navigator.NewADBExecutor(
			f.config.AndroidDevice,
			detector.NewExecRunner(),
		), nil

	case "web":
		return navigator.NewPlaywrightExecutor(
			f.config.WebURL,
			detector.NewExecRunner(),
		), nil

	case "desktop":
		display := f.config.DesktopDisplay
		if display == "" {
			display = ":0"
		}
		return navigator.NewX11Executor(
			display,
			detector.NewExecRunner(),
		), nil

	case "cli":
		cmd := f.config.CLICommand
		if cmd == "" {
			cmd = "bash"
		}
		return navigator.NewCLIExecutor(
			cmd, nil, detector.NewExecRunner(),
		), nil

	case "api":
		url := f.config.APIURL
		if url == "" {
			url = "http://localhost:8080"
		}
		return navigator.NewAPIExecutor(
			url, detector.NewExecRunner(),
		), nil

	default:
		return nil, fmt.Errorf("unsupported platform: %q", platform)
	}
}
