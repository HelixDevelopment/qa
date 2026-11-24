# Test Bank Schema Reference

HelixQA test banks are YAML files that define structured test cases. Each bank file contains metadata about the bank itself and an ordered list of test cases with steps, platform targeting, priority, and tags.

This page is the authoritative schema reference. For a practical guide on writing test banks, see [Test Banks Guide](/guides/test-banks).

## File Format

Test bank files use the `.yaml` extension and follow standard YAML 1.2 syntax. HelixQA loads banks from individual files or by scanning directories recursively for all `.yaml` files.

```bash
# Load a single bank file
helixqa run --banks banks/app-navigation.yaml

# Load all banks from a directory
helixqa run --banks banks/

# Load multiple paths (comma-separated)
helixqa run --banks banks/navigation.yaml,banks/edge-cases.yaml
```

---

## Top-Level Structure

Every bank file has three required top-level fields and one optional field.

```yaml
version: "1.0"
name: "Bank Name"
description: "What this bank tests"
metadata:                          # optional
  author: "team-name"
  app: "MyApp"
  version: "1.0.0"

test_cases:
  - id: TC-001
    name: "First test case"
    # ... (see Test Case Fields below)
```

### Top-Level Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `version` | string | Yes | Bank file format version. Currently `"1.0"`. |
| `name` | string | Yes | Human-readable name identifying this bank. |
| `description` | string | Yes | Explains the purpose and scope of the bank. |
| `metadata` | map | No | Arbitrary key-value pairs for organizational data (author, app name, version, etc.). |
| `test_cases` | list | Yes | Ordered list of test case definitions. |

---

## Test Case Fields

Each entry in the `test_cases` list defines a single test case.

```yaml
test_cases:
  - id: NAV-MAIN-001
    name: "Navigate to Files screen"
    description: "Verify the Files screen is reachable from main navigation"
    category: functional
    priority: critical
    platforms: [android, web, desktop]
    steps:
      - name: "Launch app"
        action: "Open the application"
        expected: "App launches to default screen"
      - name: "Tap Files tab"
        action: "Tap/click 'Files' in bottom navigation bar"
        expected: "Files screen displayed with file browser"
    dependencies: []
    documentation_refs:
      - type: user_guide
        section: "1.1"
        path: "docs/USER_MANUAL.md"
    tags: [navigation, main-screen, files]
    estimated_duration: "10s"
    expected_result: "Files screen accessible from main navigation"
```

### Field Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Unique identifier for this test case. Convention: `PREFIX-GROUP-NNN` (e.g., `NAV-MAIN-001`). |
| `name` | string | Yes | Human-readable name displayed in reports and list output. |
| `description` | string | No | Longer explanation of what this test validates. |
| `category` | string | No | Groups related tests for filtering. See [Valid Categories](#valid-categories). |
| `priority` | string | No | Scheduling importance. See [Valid Priorities](#valid-priorities). |
| `platforms` | list | No | Target platforms. Empty list means all platforms. See [Valid Platforms](#valid-platforms). |
| `steps` | list | Yes | Ordered list of test steps to execute. See [Step Fields](#step-fields). |
| `dependencies` | list | No | IDs of test cases that must pass before this one runs. |
| `documentation_refs` | list | No | References to project documentation for consistency verification. See [Documentation References](#documentation-references). |
| `tags` | list | No | Free-form string labels for filtering (e.g., `smoke`, `regression`, `login`). |
| `estimated_duration` | string | No | Expected execution time as a human-readable string (e.g., `"10s"`, `"2m"`). |
| `expected_result` | string | No | Summary of the expected outcome when the test passes. |

---

## Step Fields

Each test case contains an ordered list of steps. Steps are executed sequentially. A screenshot is captured after each step, and crash detection runs continuously between steps.

```yaml
steps:
  - name: "Launch app"
    action: "Open the application"
    expected: "App launches to default screen"
  - name: "Tap login button"
    action: "Tap the 'Login' button on the home screen"
    expected: "Login form displayed with username and password fields"
    platform: android    # optional: limit this step to a specific platform
```

### Field Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Short identifier for this step, used in screenshot naming (e.g., `test-001-login-step-1.png`). |
| `action` | string | Yes | Natural language description of the action to perform. The executor interprets this to generate platform-specific commands. |
| `expected` | string | Yes | Natural language description of the expected outcome after the action completes. Used for validation. |
| `platform` | string | No | Limits this step to a specific platform. When set, the step is skipped on other platforms. When omitted, the step runs on all platforms targeted by the parent test case. |

### Writing Effective Actions

Actions should be descriptive enough for the LLM-driven executor to translate into platform-specific commands:

| Good | Too Vague |
|------|-----------|
| `"Tap the 'Login' button in the top-right corner"` | `"Login"` |
| `"Type 'admin@example.com' into the email field"` | `"Enter email"` |
| `"Scroll down until the 'Save' button is visible"` | `"Find save"` |
| `"Wait for the loading spinner to disappear"` | `"Wait"` |

---

## Valid Categories

Categories group test cases for filtering with `helixqa list --category <value>`.

| Category | Description |
|----------|-------------|
| `functional` | Core feature correctness (login, navigation, CRUD operations) |
| `security` | Authentication bypass attempts, input injection, token handling |
| `edge_case` | Boundary conditions, empty states, network failure, malformed input |
| `performance` | Page load time, memory growth, response latency |
| `accessibility` | Color contrast, touch target sizes, screen reader labels |
| `visual` | Layout correctness, icon rendering, responsive breakpoints |
| `integration` | Cross-component interactions, API-to-UI consistency |

Categories are free-form strings. The values above are conventions used by the autonomous planner, but you can use any string.

---

## Valid Priorities

Priorities determine execution order and are used for filtering with `helixqa list --priority <value>`.

| Priority | Execution Order | Description |
|----------|----------------|-------------|
| `critical` | First | Must-pass tests. Failures block the rest of the suite in strict mode. |
| `high` | Second | Important functional tests covering core user flows. |
| `medium` | Third | Standard coverage tests for secondary features. |
| `low` | Last | Nice-to-have tests, cosmetic checks, minor edge cases. |

---

## Valid Platforms

Platform values control which executor runs a test case. Use `helixqa list --platform <value>` to filter.

| Platform | Executor | Description |
|----------|----------|-------------|
| `android` | ADB | Android phones and tablets |
| `web` | Playwright | Web browsers (Chromium, Firefox, WebKit) |
| `desktop` | X11 / xdotool | Linux desktop applications |

When the `platforms` field is omitted or set to an empty list, the test case targets all platforms.

```yaml
# Runs on all platforms
platforms: []

# Runs on all platforms (equivalent)
# (omit the field entirely)

# Android only
platforms: [android]

# Android and web
platforms: [android, web]
```

---

## Documentation References

The `documentation_refs` field links test cases to project documentation. During the analysis phase, HelixQA can cross-reference test results against documented behavior to detect inconsistencies.

```yaml
documentation_refs:
  - type: user_guide
    section: "3.2"
    path: "docs/USER_MANUAL.md"
  - type: api_spec
    section: "POST /api/v1/login"
    path: "docs/api/auth.md"
  - type: video_course
    section: "Module 2: Navigation"
```

### Documentation Reference Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | Yes | Documentation type: `user_guide`, `api_spec`, `video_course`, `architecture`, or any custom string. |
| `section` | string | Yes | Specific section, heading, or page reference within the document. |
| `path` | string | No | File path or URL to the document. Relative paths are resolved from the project root. |

---

## Complete Example

A full bank file demonstrating every field:

```yaml
# SPDX-FileCopyrightText: 2026 Your Name
# SPDX-License-Identifier: Apache-2.0
#
# HelixQA Test Bank: Authentication Flows

version: "1.0"
name: "Authentication Test Bank"
description: "Tests for login, logout, session management, and token refresh"
metadata:
  author: "qa-team"
  app: "Catalogizer"
  version: "2.0.0"
  last_updated: "2026-03-27"

test_cases:
  - id: AUTH-001
    name: "Successful login with valid credentials"
    description: "Verify that a user can log in with correct username and password"
    category: functional
    priority: critical
    platforms: [android, web, desktop]
    steps:
      - name: "Navigate to login screen"
        action: "Open the application and wait for the login screen to appear"
        expected: "Login screen displayed with username field, password field, and submit button"
      - name: "Enter credentials"
        action: "Type '{{QA_USERNAME}}' into the username field and '{{QA_PASSWORD}}' into the password field"
        expected: "Username and password fields populated, submit button enabled"
      - name: "Submit login form"
        action: "Tap/click the 'Login' or 'Sign In' button"
        expected: "Login succeeds, redirected to dashboard or home screen"
      - name: "Verify authenticated state"
        action: "Check that the navigation shows the user menu or profile icon"
        expected: "User menu visible, username displayed"
    dependencies: []
    documentation_refs:
      - type: user_guide
        section: "Getting Started"
        path: "docs/user-guide.md"
      - type: api_spec
        section: "POST /api/v1/auth/login"
        path: "docs/api/auth.md"
    tags: [auth, login, smoke, critical-path]
    estimated_duration: "15s"
    expected_result: "User is authenticated and sees the main application screen"

  - id: AUTH-002
    name: "Login fails with invalid password"
    description: "Verify that incorrect credentials produce a clear error message"
    category: security
    priority: high
    platforms: [android, web, desktop]
    steps:
      - name: "Navigate to login screen"
        action: "Open the application"
        expected: "Login screen displayed"
      - name: "Enter invalid credentials"
        action: "Type 'admin' into username and 'wrongpassword' into password"
        expected: "Fields populated"
      - name: "Submit login form"
        action: "Tap/click the submit button"
        expected: "Error message displayed: 'Invalid credentials' or similar"
      - name: "Verify still on login screen"
        action: "Check that the login form is still visible"
        expected: "Login screen shown, password field cleared, no navigation to app"
    tags: [auth, login, negative-test, security]
    estimated_duration: "10s"
    expected_result: "Login rejected with clear error message, user remains on login screen"

  - id: AUTH-003
    name: "Session persists across app restart"
    description: "Verify that the user remains logged in after closing and reopening the app"
    category: functional
    priority: high
    platforms: [android, desktop]
    steps:
      - name: "Log in"
        action: "Complete a successful login"
        expected: "Dashboard visible"
      - name: "Close the application"
        action: "Force-close the app (Android: swipe from recents; Desktop: close window)"
        expected: "App process terminated"
      - name: "Reopen the application"
        action: "Launch the app again"
        expected: "App opens to dashboard without requiring login"
    dependencies: [AUTH-001]
    tags: [auth, session, persistence, restart]
    estimated_duration: "30s"
    expected_result: "User session token persists; no re-login required after restart"
```

---

## Validation Rules

HelixQA validates bank files on load. A test case is invalid if:

- `id` is empty or missing
- `name` is empty or missing
- `steps` list is empty (a test case must have at least one step)
- A step is missing `name`, `action`, or `expected`

Invalid test cases produce an error message and prevent the run from starting. Use `helixqa list --banks <path>` to verify that your banks load correctly before running.

## Related Pages

- [Test Banks Guide](/guides/test-banks) -- practical guide to writing test banks
- [CLI Reference](/reference/cli) -- `helixqa run` and `helixqa list` flags
- [Pipeline Phases](/pipeline) -- how test banks feed into the execution phase
- [Configuration](/reference/config) -- environment variables for platform targeting
