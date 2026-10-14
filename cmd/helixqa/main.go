// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Command helixqa is the CLI entry point for the HelixQA
// testing framework. It supports subcommands for running QA
// pipelines, validating test banks, generating reports, and
// listing test cases.
//
// Usage:
//
//	helixqa run    --banks <paths> [flags]
//	helixqa list   --banks <paths> [--platform <p>]
//	helixqa report --input <dir>   [--format <fmt>]
//	helixqa version
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	osexec "os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"digital.vasic.challenges/pkg/logging"

	"digital.vasic.helixqa/pkg/autonomous"
	"digital.vasic.helixqa/pkg/conduit"
	"digital.vasic.helixqa/pkg/config"
	"digital.vasic.helixqa/pkg/controller"
	"digital.vasic.helixqa/pkg/helixqa"
	qainfra "digital.vasic.helixqa/pkg/infra"
	"digital.vasic.helixqa/pkg/llm"
	"digital.vasic.helixqa/pkg/memory"
	"digital.vasic.helixqa/pkg/orchestrator"
	"digital.vasic.helixqa/pkg/reporter"
	"digital.vasic.helixqa/pkg/testbank"
)

const version = "0.2.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "run":
		cmdRun(os.Args[2:])
	case "list":
		cmdList(os.Args[2:])
	case "report":
		cmdReport(os.Args[2:])
	case "autonomous":
		cmdAutonomous(os.Args[2:])
	case "http":
		cmdHTTP(os.Args[2:])
	case "replay":
		os.Exit(runReplay(os.Args[2:]))
	case "signoff":
		cmdSignoff(os.Args[2:])
	case "banks":
		cmdBanks(os.Args[2:])
	case "version":
		fmt.Printf("helixqa v%s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr,
			"error: unknown command %q\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	ctx := context.Background()
	fmt.Println(helixqaT(ctx, "helixqa_cli_banner",
		"HelixQA — AI-driven QA orchestration"))
	fmt.Println()
	fmt.Println(helixqaT(ctx, "helixqa_usage_header", "Usage:"))
	fmt.Println(helixqaT(ctx, "helixqa_usage_invocation",
		"  helixqa <command> [flags]"))
	fmt.Println()
	fmt.Println(helixqaT(ctx, "helixqa_commands_header", "Commands:"))
	fmt.Println(helixqaT(ctx, "helixqa_cmd_run_desc",
		"  run         Execute QA pipeline across platforms"))
	fmt.Println(helixqaT(ctx, "helixqa_cmd_autonomous_desc",
		"  autonomous  Run autonomous LLM-driven QA session"))
	fmt.Println(helixqaT(ctx, "helixqa_cmd_http_desc",
		"  http        Run a bank's http: cases against a live server (no browser/LLM)"))
	fmt.Println(helixqaT(ctx, "helixqa_cmd_replay_desc",
		"  replay      Replay a ticket's OCU action chain (dry-run by default)"))
	fmt.Println(helixqaT(ctx, "helixqa_cmd_list_desc",
		"  list        List test cases from banks"))
	fmt.Println(helixqaT(ctx, "helixqa_cmd_report_desc",
		"  report      Generate report from existing results"))
	fmt.Println(helixqaT(ctx, "helixqa_cmd_signoff_desc",
		"  signoff     Run release gate (Constitution §6.7)"))
	fmt.Println(helixqaT(ctx, "helixqa_cmd_banks_desc",
		"  banks       Maintain a bank directory (regen-floor)"))
	fmt.Println(helixqaT(ctx, "helixqa_cmd_version_desc",
		"  version     Print version information"))
	fmt.Println("  help        Show this help")
	fmt.Println()
	fmt.Println(helixqaT(ctx, "helixqa_cli_help_hint",
		"Run 'helixqa <command> --help' for command details."))
}

// cmdRun executes the full QA pipeline.
func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	banks := fs.String("banks", "",
		"Comma-separated test bank paths (files or directories)")
	platform := fs.String("platform", "all",
		"Target platform: android|web|desktop|all")
	device := fs.String("device", "",
		"Android device/emulator ID")
	output := fs.String("output", "qa-results",
		"Output directory for results and evidence")
	speed := fs.String("speed", "normal",
		"Speed mode: slow|normal|fast")
	reportFmt := fs.String("report", "markdown",
		"Report format: markdown|html|json")
	validate := fs.Bool("validate", true,
		"Enable step-by-step validation with crash detection")
	record := fs.Bool("record", true,
		"Enable video recording of test execution")
	verbose := fs.Bool("verbose", false,
		"Enable verbose logging")
	pkg := fs.String("package", "",
		"Android application package name")
	timeout := fs.Duration("timeout", 30*time.Minute,
		"Maximum duration for the entire run")
	browserURL := fs.String("browser-url", "",
		"URL for web platform testing")
	desktopProcess := fs.String("desktop-process", "",
		"Process name for desktop platform testing")
	tickets := fs.Bool("tickets", true,
		"Generate markdown tickets for failed tests")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if *banks == "" {
		fmt.Fprintln(os.Stderr, "error: --banks is required")
		fs.Usage()
		os.Exit(1)
	}

	platforms, err := config.ParsePlatforms(*platform)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	cfg := &config.Config{
		Banks:          config.ParseBanks(*banks),
		Platforms:      platforms,
		Device:         *device,
		PackageName:    *pkg,
		OutputDir:      *output,
		Speed:          config.SpeedMode(*speed),
		ReportFormat:   config.ReportFormat(*reportFmt),
		ValidateSteps:  *validate,
		Record:         *record,
		Verbose:        *verbose,
		Timeout:        *timeout,
		StepTimeout:    2 * time.Minute,
		BrowserURL:     *browserURL,
		DesktopProcess: *desktopProcess,
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	logger := logging.NewConsoleLogger(*verbose)
	defer logger.Close()

	orch := orchestrator.New(
		cfg,
		orchestrator.WithLogger(logger),
	)

	ctx, cancel := context.WithTimeout(
		context.Background(), cfg.Timeout,
	)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			fmt.Fprintln(os.Stderr,
				"\n"+helixqaT(ctx, "helixqa_run_interrupt_shutdown",
					"Received interrupt, shutting down..."))
			cancel()
		case <-ctx.Done():
		}
	}()

	result, err := orch.Run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Print summary. CONST-035 anti-bluff: a "PASSED" summary that
	// covers 0 challenges (because no execution engine drove the
	// prose-step banks) is exactly the "absence-of-error PASS" the
	// user mandate forbids. Distinguish three states honestly:
	//   - 0 challenges ran: report "OBSERVED — no challenges executed"
	//   - challenges ran AND all passed: report "PASSED"
	//   - any failure: report "FAILED"
	fmt.Println()
	total := 0
	if result.Report != nil {
		total = result.Report.TotalChallenges
	}
	switch {
	case total == 0:
		fmt.Println(helixqaT(ctx, "helixqa_run_summary_observed_line1",
			"OBSERVED - 0 challenges executed; crash-observation only."))
		fmt.Println(helixqaT(ctx, "helixqa_run_summary_observed_line2",
			"           This is NOT a PASS. The bank's prose steps require"))
		fmt.Println(helixqaT(ctx, "helixqa_run_summary_observed_line3",
			"           an execution backend (autonomous LLM mode or a"))
		fmt.Println(helixqaT(ctx, "helixqa_run_summary_observed_line4",
			"           concrete-action runner like Appium/UiAutomator2)."))
	case result.Success:
		fmt.Printf(helixqaT(ctx, "helixqa_run_summary_passed_fmt",
			"PASSED - All %d challenges passed, no crashes\n"), total)
	default:
		fmt.Printf(helixqaT(ctx, "helixqa_run_summary_failed_fmt",
			"FAILED - %d/%d challenges failed or crashes detected\n"),
			result.Report.FailedChallenges, total)
	}
	fmt.Printf(helixqaT(ctx, "helixqa_run_summary_report_fmt",
		"Report: %s\n"), result.ReportPath)
	fmt.Printf(helixqaT(ctx, "helixqa_run_summary_duration_fmt",
		"Duration: %v\n"), result.Duration)

	if *tickets && result.Report != nil {
		fmt.Printf("Tickets: %s/tickets/\n", cfg.OutputDir)
	}

	if !result.Success {
		os.Exit(1)
	}
}

// cmdBanks maintains a bank DIRECTORY, as opposed to running the
// cases inside it.
//
// Its only operation today is regen-floor, which rewrites a
// directory's .bank-id-floor.txt from that directory's own catalog.
// It exists as a first-class subcommand rather than a documented
// shell pipeline over `helixqa list` because the pipeline could not
// work: a floor is regenerated only after a deliberate removal —
// i.e. only while the floor check is FAILING — and on that exact
// state `list` exits 1 with nothing on stdout, so the pipeline's
// `jq | sort` saw empty input, reported success, and the `&& mv`
// replaced a 3046-id floor with a header-only file at exit 0
// (HXC-305 round-6 BLOCKING A). regen-floor reads the catalog with
// the floor check disabled by construction, refuses to write an
// empty floor, and writes atomically.
func cmdBanks(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: banks requires a subcommand")
		fmt.Fprintln(os.Stderr, "  helixqa banks regen-floor --banks <dir>")
		os.Exit(1)
	}
	switch args[0] {
	case "regen-floor":
		cmdBanksRegenFloor(args[1:])
	case "help", "-h", "--help":
		fmt.Println("Usage:")
		fmt.Println("  helixqa banks regen-floor --banks <dir>")
		fmt.Println()
		fmt.Println("Rewrites <dir>/.bank-id-floor.txt from the directory's own")
		fmt.Println("catalog, preserving the existing header. Run it in the SAME")
		fmt.Println("change as a deliberate case removal; removed ids are listed.")
	default:
		fmt.Fprintf(os.Stderr, "error: unknown banks subcommand %q\n", args[0])
		fmt.Fprintln(os.Stderr, "  helixqa banks regen-floor --banks <dir>")
		os.Exit(1)
	}
}

// cmdBanksRegenFloor regenerates one directory's id floor.
func cmdBanksRegenFloor(args []string) {
	fs := flag.NewFlagSet("banks regen-floor", flag.ExitOnError)
	banks := fs.String("banks", "",
		"Bank DIRECTORY whose .bank-id-floor.txt to regenerate")
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	if *banks == "" {
		fmt.Fprintln(os.Stderr, "error: --banks is required")
		fs.Usage()
		os.Exit(1)
	}
	info, err := os.Stat(*banks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr,
			"error: --banks must be a directory for regen-floor, got %s\n", *banks)
		os.Exit(1)
	}

	res, err := testbank.RegenerateBankIDFloor(*banks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Report the delta on stderr. A regeneration exists to record a
	// removal, so the removed ids are the fact that must be visible
	// in the terminal and in review, not merely inferable from the
	// file diff (§11.4.122).
	if res.Created {
		fmt.Fprintf(os.Stderr, "created %s with %d case id(s)\n",
			res.Path, res.Total)
	} else {
		fmt.Fprintf(os.Stderr, "rewrote %s: %d case id(s) (+%d, -%d)\n",
			res.Path, res.Total, len(res.Added), len(res.Removed))
	}
	if len(res.Removed) > 0 {
		fmt.Fprintf(os.Stderr,
			"WARNING: %d case id(s) are NO LONGER protected by this floor — "+
				"they must correspond to a deliberate removal in this same change:\n",
			len(res.Removed))
		for _, id := range res.Removed {
			fmt.Fprintf(os.Stderr, "  removed %s\n", id)
		}
	}
}

// cmdList lists test cases from banks with optional filtering.
func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	banks := fs.String("banks", "",
		"Comma-separated test bank paths")
	platform := fs.String("platform", "",
		"Filter by platform: android|web|desktop")
	category := fs.String("category", "",
		"Filter by category")
	priority := fs.String("priority", "",
		"Filter by priority: critical|high|medium|low")
	tag := fs.String("tag", "",
		"Filter by tag")
	jsonOut := fs.Bool("json", false,
		"Output as JSON")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if *banks == "" {
		fmt.Fprintln(os.Stderr, "error: --banks is required")
		fs.Usage()
		os.Exit(1)
	}

	mgr := testbank.NewManager()
	for _, path := range config.ParseBanks(*banks) {
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
		} else {
			if err := mgr.LoadFile(path); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		}
	}

	// Apply filters.
	var cases []*testbank.TestCase
	switch {
	case *platform != "":
		p, err := config.ParsePlatforms(*platform)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		cases = mgr.ForPlatform(p[0])
	case *category != "":
		cases = mgr.ByCategory(*category)
	case *priority != "":
		cases = mgr.ByPriority(testbank.Priority(*priority))
	case *tag != "":
		cases = mgr.ByTag(*tag)
	default:
		cases = mgr.All()
	}

	if *jsonOut {
		data, _ := json.MarshalIndent(cases, "", "  ")
		fmt.Println(string(data))
		return
	}

	fmt.Printf("Test cases: %d\n\n", len(cases))
	fmt.Printf("%-12s %-40s %-12s %-10s %s\n",
		"ID", "NAME", "CATEGORY", "PRIORITY", "PLATFORMS")
	fmt.Println(strings.Repeat("-", 90))
	for _, tc := range cases {
		platforms := "all"
		if len(tc.Platforms) > 0 {
			ps := make([]string, len(tc.Platforms))
			for i, p := range tc.Platforms {
				ps[i] = string(p)
			}
			platforms = strings.Join(ps, ",")
		}
		fmt.Printf("%-12s %-40s %-12s %-10s %s\n",
			tc.ID, truncate(tc.Name, 40),
			tc.Category, tc.Priority, platforms,
		)
	}
}

// cmdReport generates a report from existing QA results.
func cmdReport(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	input := fs.String("input", "qa-results",
		"Input directory containing QA results")
	format := fs.String("format", "markdown",
		"Report format: markdown|html|json")
	output := fs.String("output", "",
		"Output file path (default: auto-generated)")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	// Read existing JSON report if available.
	jsonPath := fmt.Sprintf("%s/qa-report.json", *input)
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"error: cannot read %s: %v\n"+
				"Run 'helixqa run' first to generate results.\n",
			jsonPath, err,
		)
		os.Exit(1)
	}

	var qaReport reporter.QAReport
	if err := json.Unmarshal(data, &qaReport); err != nil {
		fmt.Fprintf(os.Stderr,
			"error: invalid report: %v\n", err,
		)
		os.Exit(1)
	}

	rep := reporter.New(
		reporter.WithOutputDir(*input),
		reporter.WithReportFormat(config.ReportFormat(*format)),
	)

	outPath := *output
	if outPath == "" {
		outPath = *input
	}

	path, err := rep.WriteReport(&qaReport, outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf(helixqaT(context.Background(),
		"helixqa_report_generated_fmt",
		"Report generated: %s\n"), path)
}

// cmdAutonomous runs an autonomous LLM-driven QA session.
func cmdAutonomous(args []string) {
	fs := flag.NewFlagSet("autonomous", flag.ExitOnError)
	project := fs.String("project", ".",
		"Path to the project root")
	platforms := fs.String("platforms", "android,desktop,web",
		"Comma-separated platforms to test")
	envFile := fs.String("env", ".env",
		"Path to .env configuration file")
	timeout := fs.Duration("timeout", 2*time.Hour,
		"Maximum session duration")
	coverageTarget := fs.Float64("coverage-target", 0.9,
		"Desired feature coverage (0-1)")
	output := fs.String("output", "qa-results",
		"Output directory for results")
	reportFmts := fs.String("report", "markdown,html,json",
		"Comma-separated report formats")
	verbose := fs.Bool("verbose", false,
		"Enable verbose logging")
	curiosity := fs.Bool("curiosity", true,
		"Enable curiosity-driven exploration phase")
	curiosityTimeout := fs.Duration("curiosity-timeout",
		30*time.Minute,
		"Timeout for curiosity-driven phase")

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	// Load .env file — sets environment variables for LLM
	// API keys and platform configuration without requiring
	// the caller to export them manually.
	if err := loadEnvFile(*envFile); err != nil {
		fmt.Fprintf(os.Stderr,
			"warning: could not load env file %s: %v\n",
			*envFile, err)
	}

	fmt.Println("HelixQA Autonomous QA Session")
	fmt.Println()
	fmt.Printf("Project:          %s\n", *project)
	fmt.Printf("Platforms:        %s\n", *platforms)
	fmt.Printf("Env file:         %s\n", *envFile)
	fmt.Printf("Timeout:          %v\n", *timeout)
	fmt.Printf("Coverage target:  %.0f%%\n",
		*coverageTarget*100)
	fmt.Printf("Output:           %s\n", *output)
	fmt.Printf("Report formats:   %s\n", *reportFmts)
	fmt.Printf("Curiosity:        %v (timeout: %v)\n",
		*curiosity, *curiosityTimeout)
	fmt.Printf("Verbose:          %v\n", *verbose)
	fmt.Println()

	// Parse platforms.
	platformList, err := config.ParsePlatforms(*platforms)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	platformStrs := make([]string, len(platformList))
	for i, p := range platformList {
		platformStrs[i] = string(p)
	}

	fmt.Printf("Resolved platforms: %v\n", platformStrs)
	fmt.Println()

	// ── LLM provider setup ────────────────────────────────────────
	// Build provider configs from environment variables.
	// Configs are split into vision-capable and chat-capable
	// pools so the pipeline can use the best model for each
	// purpose without hardcoding preferences.
	var providerConfigs []llm.ProviderConfig
	var visionConfigs []llm.ProviderConfig
	var chatConfigs []llm.ProviderConfig

	// Known vision-capable providers (models that support
	// image input for screenshot analysis).
	visionProviders := map[string]bool{
		llm.ProviderAnthropic: true,
		llm.ProviderOpenAI:    true,
		llm.ProviderGoogle:    true,
		llm.ProviderOllama:    true,
		"astica":              true,
		"qwen":                true,
		"kimi":                true,
		"stepfun":             true,
		"nvidia":              true,
		"githubmodels":        true,
		"xai":                 true,
	}

	// Auto-discover all LLM providers from environment variables.
	// Supports 40+ providers via the registry in pkg/llm.
	for providerName, envKey := range llm.ProviderEnvKeys {
		val := os.Getenv(envKey)
		if val == "" {
			continue
		}
		var cfg llm.ProviderConfig
		if providerName == llm.ProviderOllama {
			// Ollama uses URL, not API key
			cfg = llm.ProviderConfig{
				Name:    providerName,
				BaseURL: val,
				Model:   os.Getenv("HELIX_OLLAMA_MODEL"),
			}
		} else {
			cfg = llm.ProviderConfig{
				Name:   providerName,
				APIKey: val,
			}
		}
		providerConfigs = append(providerConfigs, cfg)

		// Classify into vision and chat pools.
		if visionProviders[providerName] {
			visionConfigs = append(visionConfigs, cfg)
		}
		// All providers can do chat.
		chatConfigs = append(chatConfigs, cfg)
	}

	// ── Operator-supplied OpenAI-compatible vision endpoint ──
	// HELIX_VISION_ENDPOINT (+ optional HELIX_VISION_MODEL,
	// HELIX_VISION_API_KEY) points at e.g. a local llama-server
	// (llmctl `vision` on http://127.0.0.1:8082/v1). It joins the
	// vision pool; the key is never printed.
	if vcfg, ok := llm.VisionEndpointConfigFromEnv(os.Getenv); ok {
		providerConfigs = append(providerConfigs, vcfg)
		visionConfigs = append(visionConfigs, vcfg)
		fmt.Printf("Vision endpoint:  %s\n", llm.DescribeVisionEndpoint(vcfg))
	}

	// ── Bridged CLI model discovery ──────────────────────────
	// Discover CLI-based LLM tools (claude, qwen-coder,
	// opencode) that can be used as providers with zero
	// API key cost. Each discovered CLI is wrapped in a
	// BridgedCLIProvider and added to the provider pools.
	var bridgedProviders []*llm.BridgedCLIProvider
	for _, cli := range []struct {
		name  string
		bin   string
		model string
	}{
		{"claude", "claude", ""},
		{"qwen-coder", "qwen-coder", ""},
		{"opencode", "opencode", ""},
	} {
		cliPath, lookErr := osexec.LookPath(cli.bin)
		if lookErr != nil {
			continue
		}
		bp := llm.NewBridgedCLIProvider(
			cliPath, cli.name, cli.model,
		)
		bridgedProviders = append(bridgedProviders, bp)

		// Bridged CLIs are chat-capable; only Claude
		// supports vision.
		chatConfigs = append(chatConfigs,
			llm.ProviderConfig{
				Name: "bridge-" + cli.name,
			},
		)
		if bp.SupportsVision() {
			visionConfigs = append(visionConfigs,
				llm.ProviderConfig{
					Name: "bridge-" + cli.name,
				},
			)
		}
		fmt.Printf(
			"Bridged CLI:      %s (%s)\n",
			cli.name, cliPath,
		)
	}

	if len(providerConfigs) == 0 &&
		len(bridgedProviders) == 0 {
		fmt.Fprintln(os.Stderr,
			"error: no LLM providers configured — set at least one "+
				"API key env var (e.g., ANTHROPIC_API_KEY, OPENAI_API_KEY, "+
				"OPENROUTER_API_KEY, DEEPSEEK_API_KEY, GROQ_API_KEY, etc.) "+
				"or install a CLI tool (claude, qwen-coder, opencode)")
		os.Exit(1)
	}

	// Build the shared provider from all discovered configs.
	// If only bridged CLIs are available, create a minimal
	// config list so the adaptive provider has at least one
	// entry.
	if len(providerConfigs) == 0 && len(bridgedProviders) > 0 {
		// SKIP-OK anti-bluff: seeds the adaptive provider
		// with the FIRST bridged provider's name so it has
		// >= 1 entry; the bridged providers themselves are
		// the real source of capability and are wired into
		// allProviders directly below. NOT a stub/simulation
		// per CONST-035 §11.4. Whitelist documented in
		// challenges/scripts/helixqa_orchestrator_challenge.sh
		// phase 7 grep filter.
		providerConfigs = append(providerConfigs,
			llm.ProviderConfig{
				Name: bridgedProviders[0].Name(),
			},
		)
	}
	// Use enhanced adaptive provider with rate limiting and prompt optimization
	provider, err := llm.NewEnhancedAdaptiveProvider(providerConfigs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: LLM setup: %v\n", err)
		os.Exit(1)
	}

	// Build dedicated vision provider from vision-capable configs.
	var visionProvider llm.Provider
	if len(visionConfigs) > 0 {
		visionProvider, _ = llm.NewEnhancedAdaptiveProvider(visionConfigs)
	}

	// Build dedicated chat provider from all configs (chat
	// models prioritize reasoning quality over vision).
	var chatProvider llm.Provider
	if len(chatConfigs) > 0 {
		chatProvider, _ = llm.NewEnhancedAdaptiveProvider(chatConfigs)
	}

	// ── Memory store setup ────────────────────────────────────────
	dbPath := filepath.Join(*project, "HelixQA", "data", "memory.db")

	store, err := memory.NewStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"error: open memory store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	latestPass, err := store.LatestPassNumber()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"error: query pass number: %v\n", err)
		os.Exit(1)
	}
	passNumber := latestPass + 1

	// ── Print session bootstrap info ─────────────────────────────
	fmt.Printf("Pass number:      %d\n", passNumber)
	fmt.Printf("LLM provider:     %s\n", provider.Name())
	fmt.Printf("Platforms:        %v\n", platformStrs)
	fmt.Printf("Memory DB:        %s\n", dbPath)
	fmt.Println()

	cfg := &autonomous.PipelineConfig{
		ProjectRoot: *project,
		Platforms:   platformStrs,
		OutputDir: filepath.Join(
			*output,
			fmt.Sprintf("session-%d", time.Now().Unix()),
		),
		IssuesDir: filepath.Join(
			*project, "docs", "issues",
		),
		BanksDir: filepath.Join(
			*project, "challenges", "helixqa-banks",
		),
		Timeout:              *timeout,
		PassNumber:           passNumber,
		AndroidDevice:        os.Getenv("HELIX_ANDROID_DEVICE"),
		AndroidDevices:       detectADBDevices(*project),
		AndroidPackage:       os.Getenv("HELIX_ANDROID_PACKAGE"),
		WebURL:               os.Getenv("HELIX_WEB_URL"),
		DesktopDisplay:       os.Getenv("HELIX_DESKTOP_DISPLAY"),
		FFmpegPath:           os.Getenv("HELIX_FFMPEG_PATH"),
		CuriosityEnabled:     *curiosity,
		CuriosityTimeout:     *curiosityTimeout,
		VisionHost:           os.Getenv("HELIX_VISION_HOST"),
		VisionUser:           os.Getenv("HELIX_VISION_USER"),
		VisionModel:          os.Getenv("HELIX_VISION_MODEL"),
		UseLlamaCpp:          os.Getenv("HELIX_LLAMACPP") == "true",
		LlamaCppModelPath:    os.Getenv("HELIX_LLAMACPP_MODEL"),
		LlamaCppMMProjPath:   os.Getenv("HELIX_LLAMACPP_MMPROJ"),
		LlamaCppFreeGPU:      os.Getenv("HELIX_LLAMACPP_FREE_GPU") == "true",
		VisionMultiUser:      os.Getenv("HELIX_VISION_MULTI_USER"),
		LlamaCppRPCModelPath: os.Getenv("HELIX_LLAMACPP_RPC_MODEL"),
	}
	// Parse HELIX_VISION_HOSTS (comma-separated).
	if hostsEnv := os.Getenv("HELIX_VISION_HOSTS"); hostsEnv != "" {
		for _, h := range strings.Split(hostsEnv, ",") {
			h = strings.TrimSpace(h)
			if h != "" {
				cfg.VisionHosts = append(cfg.VisionHosts, h)
			}
		}
	}
	// Parse HELIX_COMPETING_APP_PACKAGES (comma-separated) — apps
	// the caller wants proactively force-stopped before structured
	// and curiosity phases, so stray Android TV home channel taps
	// do not silently hand control to a foreign app.
	if compEnv := os.Getenv("HELIX_COMPETING_APP_PACKAGES"); compEnv != "" {
		for _, p := range strings.Split(compEnv, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				cfg.CompetingAppPackages = append(cfg.CompetingAppPackages, p)
			}
		}
	}
	pipeline := autonomous.NewSessionPipeline(
		cfg, provider, store,
	)
	// Wire dedicated providers for dual-model selection.
	// Vision provider handles Execute/Curiosity phases;
	// chat provider handles Plan/Analyze phases.
	if visionProvider != nil {
		pipeline.WithVisionProvider(visionProvider)
		fmt.Printf("Vision provider:  %s\n", visionProvider.Name())
	}
	if chatProvider != nil {
		pipeline.WithChatProvider(chatProvider)
		fmt.Printf("Chat provider:    %s\n", chatProvider.Name())
	}

	// Build phase-aware model selector from all discovered
	// providers. This scores each provider per phase
	// (vision, JSON, reasoning) so the pipeline
	// automatically picks the best model for each phase.
	var allProviders []llm.Provider
	if visionProvider != nil {
		// Try to get underlying providers from enhanced adaptive provider
		if eap, ok := visionProvider.(*llm.EnhancedAdaptiveProvider); ok {
			for _, p := range eap.Providers() {
				allProviders = append(allProviders, p)
			}
		} else if ap, ok := visionProvider.(*llm.AdaptiveProvider); ok {
			for _, p := range ap.Providers() {
				allProviders = append(allProviders, p)
			}
		} else {
			allProviders = append(allProviders, visionProvider)
		}
	}
	if chatProvider != nil {
		// Try to get underlying providers from enhanced adaptive provider
		if eap, ok := chatProvider.(*llm.EnhancedAdaptiveProvider); ok {
			for _, p := range eap.Providers() {
				allProviders = append(allProviders, p)
			}
		} else if ap, ok := chatProvider.(*llm.AdaptiveProvider); ok {
			for _, p := range ap.Providers() {
				allProviders = append(allProviders, p)
			}
		} else {
			allProviders = append(allProviders, chatProvider)
		}
	}
	// Add bridged CLI providers to the allProviders pool.
	for _, bp := range bridgedProviders {
		allProviders = append(allProviders, bp)
	}
	// Deduplicate — vision and chat pools may overlap.
	allProviders = deduplicateProviders(allProviders)
	if len(allProviders) > 0 {
		phaseSelector := llm.NewPhaseModelSelector(
			allProviders,
		)
		pipeline.WithPhaseSelector(phaseSelector)
		fmt.Printf(
			"Phase selector:   %d providers\n",
			len(allProviders),
		)
	}

	// Attach QA Process Controller watchdog. Monitors
	// curiosity steps and kills stuck ones that exceed
	// the stale threshold with no heartbeat.
	ctrl := controller.New(controller.DefaultConfig())
	pipeline.WithController(ctrl)
	fmt.Println("Process ctrl:     enabled (90s stale threshold)")

	// ── Real-time conductor channel ─────────────────────────────
	// Emit a structured JSONL event stream + status snapshot into
	// the output dir so an external conductor (operator or another
	// orchestrating agent) can tail the session live and stay in
	// sync. Disabled with HELIXQA_CONDUIT=0.
	if os.Getenv("HELIXQA_CONDUIT") != "0" {
		if cw, cerr := conduit.NewWriter(conduit.Config{
			Session: fmt.Sprintf("helixqa-%d", time.Now().Unix()),
			Dir:     cfg.OutputDir,
		}); cerr != nil {
			fmt.Fprintf(os.Stderr,
				"warning: conduit channel disabled: %v\n", cerr)
		} else {
			pipeline.WithEventSink(cw)
			defer cw.Close()
			fmt.Printf("Conduit stream:   %s\n", cw.StreamPath())
			fmt.Printf("Conduit status:   %s\n", cw.StatusPath())
		}
	}

	// ── QA Infrastructure boot ──────────────────────────────────
	// When HELIX_INFRA_HOST is set, use the Containers module
	// to verify that backend services (database, cache, API) are
	// healthy before starting the pipeline. The API service name,
	// port, and health path are read from env so HelixQA stays
	// decoupled from any project-specific naming.
	if infraHost := os.Getenv("HELIX_INFRA_HOST"); infraHost != "" {
		fmt.Printf("Infra host:       %s\n", infraHost)
		apiName := os.Getenv("HELIX_INFRA_API_SERVICE")
		apiPort := os.Getenv("HELIX_INFRA_API_PORT")
		apiHealth := os.Getenv("HELIX_INFRA_API_HEALTH_PATH")
		infraCfg := qainfra.DefaultQAInfraConfig(infraHost, apiName, apiPort, apiHealth)
		infraMgr, infraErr := qainfra.NewQAInfraManager(infraCfg)
		if infraErr != nil {
			fmt.Fprintf(os.Stderr,
				"warning: infra manager: %v\n", infraErr)
		} else {
			infraCtx, infraCancel := context.WithTimeout(
				context.Background(), 30*time.Second,
			)
			_, infraBootErr := infraMgr.Boot(infraCtx)
			infraCancel()
			if infraBootErr != nil {
				fmt.Fprintf(os.Stderr,
					"warning: infra boot: %v\n",
					infraBootErr)
			}
		}
	}

	fmt.Println()
	result, err := pipeline.Run(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"error: pipeline failed: %v\n", err)
		os.Exit(1)
	}
	if err := pipeline.WriteReport(result); err != nil {
		fmt.Fprintf(os.Stderr,
			"warning: could not write report: %v\n", err)
	}
	if result.Status == autonomous.StatusFailed {
		fmt.Fprintf(os.Stderr,
			"Session failed: %s\n", result.Error)
		os.Exit(1)
	}
	// Structured bank outcome -> exit code (1 a case FAILED, 2 a case
	// BLOCKED / could not determine / banks not runnable; 1 outranks 2).
	if code := result.ExitCode(); code != 0 {
		if s := result.Structured; s != nil {
			fmt.Fprintf(os.Stderr,
				"Structured cases: %d passed, %d failed, %d blocked (could not determine), %d skipped — exit %d\n",
				s.TestCasesPassed, s.TestCasesFailed, s.TestCasesBlocked, s.TestCasesSkipped, code)
		}
		if result.StructuredError != "" {
			fmt.Fprintf(os.Stderr, "Structured phase could not run: %s\n", result.StructuredError)
		}
		os.Exit(code)
	}
}

// detectADBDevices runs `adb devices` and returns all
// connected device serials. Falls back to HELIX_ANDROID_DEVICE
// env var if no devices are detected.
// The projectRoot parameter is used to locate the .devignore file.
func detectADBDevices(projectRoot string) []string {
	out, err := osexec.Command(
		"adb", "devices",
	).Output()
	if err != nil {
		// Fall back to env var.
		if dev := os.Getenv("HELIX_ANDROID_DEVICE"); dev != "" {
			return []string{dev}
		}
		return nil
	}
	// Load device exclusions from .devignore file (project root)
	// and HELIX_ADB_EXCLUDE env var. Case-insensitive substring
	// match against `adb devices -l` output.
	var excludeModels []string

	// Read .devignore from project root (try multiple locations).
	// First try the project root, then relative paths from working directory.
	devignorePaths := []string{
		filepath.Join(projectRoot, ".devignore"),
		".devignore",
		"../.devignore",
		"../../.devignore",
	}
	for _, path := range devignorePaths {
		if data, err := os.ReadFile(path); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				excludeModels = append(excludeModels, strings.ToLower(line))
			}
			break
		}
	}

	// Also check HELIX_ADB_EXCLUDE env var.
	if excludeRaw := os.Getenv("HELIX_ADB_EXCLUDE"); excludeRaw != "" {
		for _, m := range strings.Split(excludeRaw, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				excludeModels = append(excludeModels, strings.ToLower(m))
			}
		}
	}

	// Also get detailed device info for filtering.
	detailOut, _ := osexec.Command("adb", "devices", "-l").Output()
	detailLines := make(map[string]string) // serial -> full line
	for _, line := range strings.Split(string(detailOut), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] == "device" {
			detailLines[parts[0]] = strings.ToLower(line)
		}
	}

	var devices []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List") ||
			strings.HasPrefix(line, "*") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] == "device" {
			serial := parts[0]
			// Check exclude list against device detail line.
			excluded := false
			if detail, ok := detailLines[serial]; ok {
				for _, exc := range excludeModels {
					if strings.Contains(detail, exc) {
						fmt.Printf("Excluding device %s (matches %q)\n", serial, exc)
						excluded = true
						break
					}
				}
			}
			if !excluded {
				devices = append(devices, serial)
			}
		}
	}
	if len(devices) > 0 {
		fmt.Printf(
			"Detected %d ADB devices: %v\n",
			len(devices), devices,
		)
	}
	return devices
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// deduplicateProviders removes duplicate providers by
// name, keeping the first occurrence.
func deduplicateProviders(
	providers []llm.Provider,
) []llm.Provider {
	seen := make(map[string]bool, len(providers))
	var unique []llm.Provider
	for _, p := range providers {
		if !seen[p.Name()] {
			seen[p.Name()] = true
			unique = append(unique, p)
		}
	}
	return unique
}

// loadEnvFile reads a .env file and sets environment variables
// for any keys not already present in the environment. Lines
// starting with # and blank lines are ignored. Supports
// KEY=VALUE format with optional quoting.
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])

		// Strip surrounding quotes if present.
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}

		// .env file values override existing environment variables
		// to ensure configuration from file takes precedence.
		os.Setenv(key, val)
	}
	return scanner.Err()
}

// cmdSignoff runs the project release gate (Constitution §6.7).
// It executes the full test matrix via the helixqa orchestrator and
// evaluates the release gate. Exit 0 = signoff granted, Exit 1 = blocked.
func cmdSignoff(args []string) {
	fs := flag.NewFlagSet("signoff", flag.ExitOnError)
	_ = fs.Float64("threshold", 0.85, "minimum mutation score")
	short := fs.Bool("short", false, "skip long-running tests")
	_ = fs.Parse(args)

	fmt.Println("=== HelixQA Release Signoff ===")
	fmt.Println("Constitution §6.7: usability evidence mandatory")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	orc, err := helixqa.NewOrchestrator()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: cannot create orchestrator: %v\n", err)
		os.Exit(1)
	}

	if *short {
		fmt.Println("Short mode: skipping stress/smoke/fullauto challenges")
	}

	allPassed := orc.RunAll(ctx)
	fmt.Println()
	fmt.Println(orc.Summary())
	if !allPassed {
		fmt.Fprintln(os.Stderr, "Some test types failed.")
	}

	gate := helixqa.NewReleaseGate()
	if err := gate.Evaluate(orc.Results()); err != nil {
		fmt.Fprintf(os.Stderr, "RELEASE GATE BLOCKED: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("RELEASE GATE: OPEN")
	fmt.Println("HelixQA signoff granted.")
}
