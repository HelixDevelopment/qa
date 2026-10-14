// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"fmt"
	"strings"
	"time"

	"digital.vasic.helixqa/pkg/detector"
	"digital.vasic.helixqa/pkg/navigator"
)

// ExecutorFactory creates platform-specific ActionExecutor
// instances. Implementations select the appropriate executor
// (ADB, Playwright, X11) based on the platform string and
// configuration.
type ExecutorFactory interface {
	// Create returns an ActionExecutor for the given platform.
	// Returns an error if the platform is unsupported or
	// required configuration is missing.
	Create(platform string) (navigator.ActionExecutor, error)
}

// ExecutorConfig holds platform-specific configuration used
// by DefaultExecutorFactory to create executors.
type ExecutorConfig struct {
	// AndroidDevice is the ADB device/emulator serial.
	AndroidDevice string

	// BrowserURL is the URL for web platform testing.
	BrowserURL string

	// DesktopDisplay is the X11 display (e.g. ":0").
	DesktopDisplay string

	// CommandRunner overrides the default os/exec runner.
	// If nil, detector.NewExecRunner() is used.
	CommandRunner detector.CommandRunner
}

// DefaultExecutorFactory creates real platform executors using
// the navigator package's ADBExecutor, PlaywrightExecutor, and
// X11Executor. It uses os/exec for command execution by default.
type DefaultExecutorFactory struct {
	config ExecutorConfig
	runner detector.CommandRunner
}

// NewDefaultExecutorFactory creates a DefaultExecutorFactory
// with the given configuration. If cfg.CommandRunner is nil,
// the factory uses detector.NewExecRunner() for real command
// execution.
func NewDefaultExecutorFactory(
	cfg ExecutorConfig,
) *DefaultExecutorFactory {
	runner := cfg.CommandRunner
	if runner == nil {
		runner = detector.NewExecRunner()
	}
	return &DefaultExecutorFactory{
		config: cfg,
		runner: runner,
	}
}

// Create returns the appropriate ActionExecutor for the
// platform. Supported platforms: "android", "desktop", "web".
func (f *DefaultExecutorFactory) Create(
	platform string,
) (navigator.ActionExecutor, error) {
	switch platform {
	case "android", "androidtv":
		if f.config.AndroidDevice == "" {
			return nil, fmt.Errorf(
				"android device ID is required; " +
					"set --device or ANDROID_DEVICE",
			)
		}
		return navigator.NewADBExecutor(
			f.config.AndroidDevice, f.runner,
		), nil

	case "web":
		if f.config.BrowserURL == "" {
			return nil, fmt.Errorf(
				"browser URL is required; " +
					"set --browser-url or WEB_URL",
			)
		}
		return navigator.NewPlaywrightExecutor(
			f.config.BrowserURL, f.runner,
		), nil

	case "desktop":
		display := f.config.DesktopDisplay
		if display == "" {
			display = ":0"
		}
		return navigator.NewX11Executor(
			display, f.runner,
		), nil

	default:
		return nil, fmt.Errorf(
			"unsupported platform: %q", platform,
		)
	}
}

// probeTimeout bounds the one reachability probe per platform.
const probeTimeout = 20 * time.Second

// Probe checks that the platform's configured device / display / browser
// actually answers (see probePlatformReachable). StructuredTestExecutor
// calls it once per platform (ExecutorProber).
func (f *DefaultExecutorFactory) Probe(ctx context.Context, platform string) error {
	return probePlatformReachable(ctx, platform, f.config.AndroidDevice, f.runner, f.Create)
}

// probePlatformReachable is the one reachability probe both production
// factories use, so a configured-but-unreachable target (an emulator that
// is gone, the default desktop display ":0" with no X server, a browser
// that cannot start) BLOCKS the steps that need it instead of being acted
// on blindly:
//
//   - android / androidtv: `adb -s <device> get-state` must print exactly
//     "device" (offline / unauthorized / not found / no serial are
//     unreachable);
//   - desktop, web: one real capture through the executor itself must
//     return bytes without error;
//   - other platforms (cli, api) run on the host and are not probed.
func probePlatformReachable(
	ctx context.Context,
	platform, androidDevice string,
	runner detector.CommandRunner,
	create func(string) (navigator.ActionExecutor, error),
) error {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	switch platform {
	case "android", "androidtv":
		if androidDevice == "" {
			return fmt.Errorf("no android device serial configured")
		}
		out, err := runner.Run(pctx, "adb", "-s", androidDevice, "get-state")
		state := strings.TrimSpace(string(out))
		if err != nil || state != "device" {
			return fmt.Errorf("adb -s %s get-state: state %q (%v) — device not reachable",
				androidDevice, truncateOutput([]byte(state), 120), err)
		}
		return nil
	case "desktop", "web":
		ex, err := create(platform)
		if err != nil {
			return err
		}
		shot, err := ex.Screenshot(pctx)
		if err != nil {
			return fmt.Errorf("%s capture probe failed: %w", platform, err)
		}
		if len(shot) == 0 {
			return fmt.Errorf("%s capture probe returned no image data", platform)
		}
		return nil
	default:
		return nil
	}
}

// NoopExecutorFactory moved to noop_executor_test.go per §11.4 /
// CONST-050(A) — test-only. Production callers must use
// DefaultExecutorFactory; a stub that silently succeeds every UI
// action without driving a real device is a PASS-bluff if reached
// from a production binary.
