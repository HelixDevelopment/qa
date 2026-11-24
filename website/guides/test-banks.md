# Test Bank Authoring Guide

Test banks are YAML files that define structured, reusable test cases for HelixQA. While autonomous mode generates tests from your codebase using an LLM, hand-written test banks give you precise control over what gets tested, in what order, and on which platforms.

This guide walks you through creating, structuring, and maintaining test banks. For the formal schema specification, see [Test Bank Schema Reference](/reference/test-bank-schema).

## When to Use Test Banks

Test banks complement autonomous mode. Use them when you need:

- **Repeatable smoke suites** that run identically every time
- **Regression tests** for specific bugs that must never recur
- **Acceptance criteria** tied to feature requirements
- **Platform-specific flows** that need exact step sequences (e.g., Android intent handling)
- **Onboarding tests** that new team members can read to understand expected behavior

Run banks directly with `helixqa run`:

```bash
helixqa run --banks banks/ --platform android --output qa-results
```

Banks are also loaded during autonomous sessions for reconciliation -- the planner avoids generating tests that duplicate existing bank coverage.

---

## Creating a New Test Bank

### File Setup

Create a `.yaml` file in your banks directory. Start with the required top-level fields:

```yaml
# SPDX-FileCopyrightText: 2026 Your Name
# SPDX-License-Identifier: Apache-2.0
#
# HelixQA Test Bank: <descriptive title>

version: "1.0"
name: "Authentication Test Bank"
description: "Login, logout, session management, and token refresh tests"
metadata:
  author: "qa-team"
  app: "MyApp"
  version: "1.0.0"

test_cases:
  # Test cases go here
```

### Naming Conventions

Organize bank files by feature area or test category:

```
banks/
  app-navigation.yaml       # Navigation flows
  authentication.yaml        # Login, logout, session
  file-browser.yaml          # File browsing and selection
  editor-operations.yaml     # Editor functionality
  edge-cases-stress.yaml     # Boundary conditions
  cloud-storage-operations.yaml  # Cloud sync
  atmosphere.yaml            # UI atmosphere and theming
```

Keep each bank focused on a single feature area. A bank with 10-30 test cases is typical. If a bank grows beyond 50 cases, consider splitting it.

---

## YAML Structure and Fields

### Test Case Anatomy

Every test case needs at minimum an `id`, a `name`, and at least one step:

```yaml
test_cases:
  - id: AUTH-001
    name: "Successful login with valid credentials"
    steps:
      - name: "Open login screen"
        action: "Launch the application"
        expected: "Login screen displayed"
      - name: "Enter credentials and submit"
        action: "Type '{{QA_USERNAME}}' into username, '{{QA_PASSWORD}}' into password, tap Login"
        expected: "Dashboard screen displayed"
```

The full set of optional fields adds precision and filtering capability:

```yaml
test_cases:
  - id: AUTH-001
    name: "Successful login with valid credentials"
    description: "Verify the happy path for user authentication"
    category: functional
    priority: critical
    platforms: [android, web, desktop]
    steps:
      - name: "Open login screen"
        action: "Launch the application"
        expected: "Login screen displayed with username and password fields"
      - name: "Enter credentials"
        action: "Type '{{QA_USERNAME}}' into the username field and '{{QA_PASSWORD}}' into the password field"
        expected: "Both fields populated, Login button enabled"
      - name: "Submit login"
        action: "Tap/click the Login button"
        expected: "Redirected to dashboard, user menu shows 'admin'"
    dependencies: []
    documentation_refs:
      - type: user_guide
        section: "Getting Started"
        path: "docs/user-guide.md"
    tags: [auth, login, smoke, critical-path]
    estimated_duration: "15s"
    expected_result: "User authenticated and on dashboard"
```

### ID Conventions

Use a prefix-group-number scheme for test case IDs:

```
<FEATURE>-<GROUP>-<NNN>
```

Examples:

| ID | Feature | Group | Number |
|----|---------|-------|--------|
| `NAV-MAIN-001` | Navigation | Main screens | 001 |
| `NAV-CTRL-003` | Navigation | Controls | 003 |
| `AUTH-001` | Authentication | (default) | 001 |
| `EDGE-NET-002` | Edge cases | Network | 002 |
| `PERF-MEM-001` | Performance | Memory | 001 |

IDs must be unique across all loaded banks. Duplicate IDs cause a load error.

---

## Writing Effective Test Steps

Steps are the heart of a test case. Each step has three fields: `name`, `action`, and `expected`. The executor uses the `action` text to determine what to do and the `expected` text to validate the result.

### Action Writing Guidelines

Write actions as clear, specific instructions that a human tester could follow without ambiguity:

```yaml
# Good: specific and unambiguous
- name: "Enter email"
  action: "Tap the email input field and type 'user@example.com'"
  expected: "Email field shows 'user@example.com'"

# Bad: vague, multiple interpretations
- name: "Enter email"
  action: "Fill in email"
  expected: "Email entered"
```

Include target identifiers when possible:

```yaml
# Good: identifies the exact element
- name: "Tap submit"
  action: "Tap the 'Save Changes' button at the bottom of the form"
  expected: "Success toast appears: 'Settings saved'"

# Bad: ambiguous when multiple buttons exist
- name: "Tap button"
  action: "Tap the button"
  expected: "Something happens"
```

### Expected Outcome Guidelines

Expected outcomes should be verifiable from a screenshot:

```yaml
# Good: visually verifiable
expected: "File list shows 3 items, each with filename and size"

# Bad: internal state that cannot be seen
expected: "Database record created successfully"
```

### Platform-Specific Steps

Use the optional `platform` field on individual steps to handle platform differences within a single test case:

```yaml
steps:
  - name: "Open navigation"
    action: "Swipe right from left edge to open drawer"
    expected: "Navigation drawer visible"
    platform: android

  - name: "Open navigation"
    action: "Click the hamburger menu icon in the top-left corner"
    expected: "Sidebar navigation panel visible"
    platform: web

  - name: "Open navigation"
    action: "Click 'Files' in the left sidebar"
    expected: "File browser panel active"
    platform: desktop

  - name: "Navigate to settings"
    action: "Tap/click 'Settings' in the navigation menu"
    expected: "Settings screen displayed"
    # No platform field: runs on all platforms
```

---

## Platform Targeting

### Targeting All Platforms

Omit the `platforms` field or set it to an empty list. The test runs on every platform passed to `--platform`:

```yaml
- id: NAV-001
  name: "Navigate to home screen"
  # platforms field omitted: runs everywhere
  steps:
    - name: "Open app"
      action: "Launch the application"
      expected: "Home screen displayed"
```

### Targeting Specific Platforms

List the target platforms explicitly:

```yaml
# Android only
platforms: [android]

# Android and web (not desktop)
platforms: [android, web]

# Desktop only
platforms: [desktop]
```

### Platform-Exclusive Test Cases

Some test cases only make sense on a single platform. Make this explicit in both the `platforms` field and the test name:

```yaml
- id: NAV-CTRL-003
  name: "System back button navigation (Android)"
  category: functional
  priority: critical
  platforms: [android]
  steps:
    - name: "Navigate into sub-screen"
      action: "Open a file in the editor"
      expected: "Editor screen visible"
    - name: "Press system back"
      action: "Press Android system back button"
      expected: "Returns to file browser"
```

---

## Priority and Category Assignment

### Priority

Assign priority based on the impact of failure:

| Priority | When to Use | Examples |
|----------|-------------|---------|
| `critical` | App is broken or unusable if this fails | Login, app launch, main navigation |
| `high` | Core feature is impaired | File save, media playback, search |
| `medium` | Feature works but degraded | Sorting, filtering, secondary settings |
| `low` | Minor inconvenience or cosmetic | Tooltip text, animation smoothness |

Start with `critical` for your smoke suite (5-10 tests covering the core user journey), then build outward with `high` and `medium` tests.

### Category

Categories group tests by type, independent of which feature they test:

| Category | Description | Examples |
|----------|-------------|---------|
| `functional` | Does the feature work? | Login, CRUD, navigation |
| `security` | Can the feature be abused? | SQL injection, auth bypass |
| `edge_case` | What happens at boundaries? | Empty input, max-length strings, offline |
| `performance` | Is it fast enough? | Load time, memory, scroll smoothness |
| `accessibility` | Can everyone use it? | Contrast, touch targets, screen reader |
| `visual` | Does it look correct? | Layout, icons, responsive |
| `integration` | Do components work together? | API-to-UI consistency, data flow |

---

## Tagging Strategy

Tags provide flexible, cross-cutting filtering beyond category and priority. Use them to create virtual test suites:

```yaml
tags: [auth, login, smoke, critical-path]
tags: [navigation, drawer, android]
tags: [editor, markdown, preview, regression]
tags: [file-browser, storage, cloud, network]
```

### Recommended Tag Categories

| Tag Type | Examples | Purpose |
|----------|---------|---------|
| Feature area | `auth`, `editor`, `file-browser`, `settings` | Group by product feature |
| Test type | `smoke`, `regression`, `exploratory` | Group by test purpose |
| Risk area | `critical-path`, `data-loss`, `security` | Group by risk level |
| Platform hint | `android`, `web`, `desktop`, `mobile` | Supplement platform filtering |
| Sprint/release | `sprint-42`, `v2.0`, `release-candidate` | Group by delivery milestone |

### Filtering by Tag

```bash
# Run only smoke tests
helixqa run --banks banks/ --filter smoke

# List all regression-tagged tests
helixqa list --banks banks/ --tag regression

# List security tests as JSON
helixqa list --banks banks/ --tag security --json
```

---

## Dependencies

The `dependencies` field lists test case IDs that must pass before the current test runs. Use this for sequential flows where later tests assume state created by earlier tests:

```yaml
- id: AUTH-001
  name: "Successful login"
  steps:
    - name: "Log in"
      action: "Complete login with valid credentials"
      expected: "Dashboard visible"

- id: PROF-001
  name: "Update profile name"
  dependencies: [AUTH-001]    # requires authenticated state
  steps:
    - name: "Open profile"
      action: "Navigate to Settings > Profile"
      expected: "Profile screen with current user data"
    - name: "Change name"
      action: "Clear the name field and type 'New Name'"
      expected: "Name field updated"
```

Keep dependency chains short (1-2 levels). Deep chains are fragile -- a single early failure blocks all downstream tests.

---

## Documentation References

Link test cases to project documentation for traceability:

```yaml
documentation_refs:
  - type: user_guide
    section: "3.2 File Browser"
    path: "docs/USER_MANUAL.md"
  - type: api_spec
    section: "GET /api/v1/files"
    path: "docs/api/files.md"
  - type: video_course
    section: "Module 4: Storage"
```

The analysis phase can cross-reference test results against documented behavior to detect inconsistencies between documentation and implementation.

---

## Validation and Testing

### Verify Banks Load Correctly

Before running a full session, verify your banks parse without errors:

```bash
# List all test cases (catches parse errors)
helixqa list --banks banks/

# List with platform filter
helixqa list --banks banks/ --platform android

# Export as JSON for inspection
helixqa list --banks banks/ --json | jq '.[] | {id, name, category, priority}'
```

### Common Validation Errors

| Error | Cause | Fix |
|-------|-------|-----|
| `test case missing ID` | `id` field is empty or absent | Add a unique ID |
| `test case X missing name` | `name` field is empty | Add a descriptive name |
| `invalid platform` | Unrecognized platform string | Use `android`, `web`, or `desktop` |
| `duplicate ID` | Same ID appears in multiple banks | Ensure IDs are globally unique |

### Dry Run

Run banks with a short timeout and verbose logging to see what happens without committing to a full session:

```bash
helixqa run \
  --banks banks/authentication.yaml \
  --platform web \
  --browser-url http://localhost:3000 \
  --timeout 2m \
  --speed slow \
  --verbose
```

---

## Bank Organization Patterns

### By Feature Area (Recommended)

One bank per major feature:

```
banks/
  authentication.yaml      # 8 test cases
  app-navigation.yaml      # 20 test cases
  file-browser.yaml        # 15 test cases
  editor-operations.yaml   # 18 test cases
  cloud-storage.yaml       # 12 test cases
  settings.yaml            # 10 test cases
```

### By Test Type

One bank per test category:

```
banks/
  smoke.yaml               # Critical path only
  functional.yaml          # Full functional suite
  edge-cases.yaml          # Boundary conditions
  security.yaml            # Security-focused tests
  accessibility.yaml       # A11y checks
```

### Hybrid

Feature banks plus cross-cutting suites:

```
banks/
  features/
    authentication.yaml
    navigation.yaml
    editor.yaml
  suites/
    smoke.yaml             # References critical tests from feature banks
    regression.yaml        # Tests for previously reported bugs
```

## Related Pages

- [Test Bank Schema Reference](/reference/test-bank-schema) -- formal YAML schema specification
- [CLI Reference](/reference/cli) -- `helixqa run` and `helixqa list` flags
- [Autonomous QA Guide](/guides/autonomous-qa) -- how banks integrate with autonomous mode
- [Pipeline Phases](/pipeline) -- how test banks feed into execution
