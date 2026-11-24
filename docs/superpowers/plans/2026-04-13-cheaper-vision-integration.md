# Cheaper Vision Integration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate low-cost and free vision model providers (UI-TARS, ShowUI, GLM-4V, Qwen2.5-VL, OmniParser) into HelixQA with a 5-layer learning system (exact cache, differential cache, vector memory, few-shot learning, provider optimization), resilient execution via failsafe-go, and full test coverage.

**Architecture:** New `pkg/vision/cheaper/` package tree provides a standalone `VisionProvider` interface with image.Image-based analysis, a registry, 5 provider adapters, a failsafe-go resilient executor (4 strategies), and a 5-layer learning pipeline using chromem-go for vector memory and go-cache for differential caching. A bridge adapter connects this to the existing `pkg/llm.Provider` interface so the autonomous pipeline can use cheaper providers seamlessly. Configuration via env vars + YAML, Prometheus metrics throughout.

**Tech Stack:** Go 1.25, failsafe-go (circuit breaker/retry/timeout/fallback), chromem-go (embedded vector DB), patrickmn/go-cache (TTL cache), google/uuid, testify, image/png stdlib

---

## File Map

### New Files (pkg/vision/cheaper/)

| File | Responsibility |
|------|---------------|
| `pkg/vision/cheaper/provider.go` | `VisionProvider` interface, `VisionResult`, `ProviderCapabilities`, `ProviderConfig` types |
| `pkg/vision/cheaper/registry.go` | Thread-safe provider factory registry (Register/Create/List/Unregister) |
| `pkg/vision/cheaper/executor.go` | `ResilientExecutor` with failsafe-go: 4 strategies (first_success, parallel, fallback, weighted), per-provider circuit breakers |
| `pkg/vision/cheaper/bridge.go` | `BridgeProvider` adapts `VisionProvider` to `pkg/llm.Provider` interface |
| `pkg/vision/cheaper/metrics.go` | Prometheus counters/histograms for vision requests, cache hits, circuit breaker state |
| `pkg/vision/cheaper/provider_test.go` | Unit tests for VisionResult, ProviderCapabilities |
| `pkg/vision/cheaper/registry_test.go` | Unit tests for registry CRUD, concurrency safety |
| `pkg/vision/cheaper/executor_test.go` | Unit tests for all 4 strategies, circuit breaker, retry, timeout |
| `pkg/vision/cheaper/bridge_test.go` | Unit tests for bridge adapter |
| `pkg/vision/cheaper/metrics_test.go` | Unit tests for metrics registration |

### New Files (pkg/vision/cheaper/adapters/)

| File | Responsibility |
|------|---------------|
| `pkg/vision/cheaper/adapters/uitars/uitars.go` | UI-TARS 1.5-7B adapter (HuggingFace Inference API, OpenAI-compatible) |
| `pkg/vision/cheaper/adapters/uitars/uitars_test.go` | Unit tests with httptest mock server |
| `pkg/vision/cheaper/adapters/showui/showui.go` | ShowUI-2B adapter (local Gradio API) |
| `pkg/vision/cheaper/adapters/showui/showui_test.go` | Unit tests with httptest mock server |
| `pkg/vision/cheaper/adapters/glm4v/glm4v.go` | GLM-4V adapter (Zhipu AI, free tier available) |
| `pkg/vision/cheaper/adapters/glm4v/glm4v_test.go` | Unit tests with httptest mock server |
| `pkg/vision/cheaper/adapters/qwen25vl/qwen25vl.go` | Qwen2.5-VL adapter (local OpenAI-compatible endpoint) |
| `pkg/vision/cheaper/adapters/qwen25vl/qwen25vl_test.go` | Unit tests with httptest mock server |
| `pkg/vision/cheaper/adapters/omniparser/omniparser.go` | OmniParser V2 adapter (Microsoft UI parsing) |
| `pkg/vision/cheaper/adapters/omniparser/omniparser_test.go` | Unit tests with httptest mock server |

### New Files (pkg/vision/cheaper/cache/)

| File | Responsibility |
|------|---------------|
| `pkg/vision/cheaper/cache/exact.go` | L1 exact image cache (SHA-256 hash -> response, thread-safe map) |
| `pkg/vision/cheaper/cache/differential.go` | L2 differential cache (24x24 patch hashing, change detection, go-cache TTL) |
| `pkg/vision/cheaper/cache/exact_test.go` | Unit tests for exact cache (hit, miss, eviction) |
| `pkg/vision/cheaper/cache/differential_test.go` | Unit tests for differential cache (identical frames, small changes, large changes) |

### New Files (pkg/vision/cheaper/memory/)

| File | Responsibility |
|------|---------------|
| `pkg/vision/cheaper/memory/vector_store.go` | chromem-go vector memory store (Store, Search, GetFewShotExamples, persistence) |
| `pkg/vision/cheaper/memory/hash.go` | Image hashing utilities (SHA-256 for exact match) |
| `pkg/vision/cheaper/memory/vector_store_test.go` | Unit tests for store/search/persistence |
| `pkg/vision/cheaper/memory/hash_test.go` | Unit tests for image hashing |

### New Files (pkg/vision/cheaper/learning/)

| File | Responsibility |
|------|---------------|
| `pkg/vision/cheaper/learning/executor.go` | `LearningVisionExecutor` — 5-layer pipeline (L1-L5) composing cache+memory+executor |
| `pkg/vision/cheaper/learning/few_shot.go` | `FewShotBuilder` — retrieves successful memories, augments prompts |
| `pkg/vision/cheaper/learning/optimizer.go` | `ProviderOptimizer` — tracks per-provider per-UI-type accuracy, recommends best provider |
| `pkg/vision/cheaper/learning/executor_test.go` | Unit tests for full learning pipeline |
| `pkg/vision/cheaper/learning/few_shot_test.go` | Unit tests for prompt augmentation |
| `pkg/vision/cheaper/learning/optimizer_test.go` | Unit tests for metric recording and provider selection |

### New Files (internal/)

| File | Responsibility |
|------|---------------|
| `internal/visionserver/server.go` | HTTP server exposing vision analysis API (/api/v1/vision/analyze, /health, /metrics, provider management) |
| `internal/visionserver/handlers.go` | HTTP handlers for each endpoint |
| `internal/visionserver/config.go` | YAML + env var configuration loader |
| `internal/visionserver/server_test.go` | Integration tests for HTTP server |
| `internal/visionserver/handlers_test.go` | Unit tests for handlers |
| `internal/visionserver/config_test.go` | Unit tests for config loading |

### New Files (docs/)

| File | Responsibility |
|------|---------------|
| `docs/vision/USER_GUIDE.md` | End-user guide (quick start, configuration, providers, learning system, monitoring) |
| `docs/vision/ADMIN_GUIDE.md` | Administrator guide (deployment, scaling, backup, security, tuning) |
| `docs/vision/API_REFERENCE.md` | REST API reference for all endpoints |

### Modified Files

| File | Change |
|------|--------|
| `go.mod` | Add failsafe-go, chromem-go, patrickmn/go-cache dependencies |
| `pkg/llm/vision_ranking.go` | Add entries for cheaper providers (uitars, showui, glm4v, qwen25vl) |
| `pkg/llm/providers_registry.go` | Register cheaper providers in the existing registry |
| `pkg/autonomous/pipeline.go` | Wire cheaper vision providers into phase model selection |

---

## Phase 1: Core Interfaces + Registry

### Task 1: VisionProvider interface and types

**Files:**
- Create: `pkg/vision/cheaper/provider.go`
- Create: `pkg/vision/cheaper/provider_test.go`

- [ ] **Step 1: Write failing test for VisionResult and ProviderCapabilities**

```go
// pkg/vision/cheaper/provider_test.go
// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package cheaper

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestVisionResult_Fields(t *testing.T) {
	r := VisionResult{
		Text:       "Login button at (100, 50)",
		Provider:   "test-provider",
		Model:      "test-model",
		Duration:   500 * time.Millisecond,
		Timestamp:  time.Now(),
		CacheHit:   false,
		Confidence: 0.95,
	}
	assert.Equal(t, "Login button at (100, 50)", r.Text)
	assert.Equal(t, "test-provider", r.Provider)
	assert.Equal(t, 0.95, r.Confidence)
	assert.False(t, r.CacheHit)
}

func TestProviderCapabilities_Defaults(t *testing.T) {
	caps := ProviderCapabilities{
		MaxImageSize:     20 * 1024 * 1024,
		SupportedFormats: []string{"png", "jpg"},
		AverageLatency:   2 * time.Second,
		CostPer1MTokens:  0.0,
	}
	assert.Equal(t, 20*1024*1024, caps.MaxImageSize)
	assert.Contains(t, caps.SupportedFormats, "png")
	assert.Equal(t, 0.0, caps.CostPer1MTokens)
}

func TestProviderConfig_Validation(t *testing.T) {
	cfg := ProviderConfig{
		Name:    "glm4v",
		Enabled: true,
		Priority: 1,
		Config: map[string]interface{}{
			"api_key": "test-key",
		},
	}
	assert.Equal(t, "glm4v", cfg.Name)
	assert.True(t, cfg.Enabled)
	assert.Equal(t, 1, cfg.Priority)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd $WORKSPACE/Catalogizer/HelixQA && go test ./pkg/vision/cheaper/ -v -run TestVisionResult`
Expected: FAIL — package does not exist

- [ ] **Step 3: Implement provider.go**

```go
// pkg/vision/cheaper/provider.go
// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package cheaper provides low-cost vision model adapters with
// a 5-layer learning system, resilient execution, and caching.
package cheaper

import (
	"context"
	"image"
	"time"
)

// VisionResult represents the output from a vision model analysis.
type VisionResult struct {
	Text        string                 `json:"text"`
	RawResponse interface{}            `json:"raw_response,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	Duration    time.Duration          `json:"duration"`
	Model       string                 `json:"model"`
	Provider    string                 `json:"provider"`
	Timestamp   time.Time              `json:"timestamp"`
	CacheHit    bool                   `json:"cache_hit,omitempty"`
	Confidence  float64                `json:"confidence,omitempty"`
}

// VisionProvider defines the interface for all cheaper vision model providers.
// Implementations must be safe for concurrent use.
type VisionProvider interface {
	// Analyze sends an image and prompt to the vision model.
	Analyze(ctx context.Context, img image.Image, prompt string) (*VisionResult, error)

	// Name returns the unique identifier for this provider.
	Name() string

	// HealthCheck verifies the provider is reachable and functioning.
	HealthCheck(ctx context.Context) error

	// GetCapabilities returns provider-specific capabilities.
	GetCapabilities() ProviderCapabilities

	// GetCostEstimate returns estimated cost for a request.
	GetCostEstimate(imageSize int, promptLength int) float64
}

// ProviderCapabilities describes what a provider can do.
type ProviderCapabilities struct {
	SupportsStreaming bool          `json:"supports_streaming"`
	MaxImageSize      int           `json:"max_image_size"`
	SupportedFormats  []string      `json:"supported_formats"`
	AverageLatency    time.Duration `json:"average_latency"`
	SupportsBatch     bool          `json:"supports_batch"`
	CostPer1MTokens   float64       `json:"cost_per_1m_tokens"`
}

// ProviderFactory creates VisionProvider instances from config.
type ProviderFactory func(config map[string]interface{}) (VisionProvider, error)

// ProviderConfig holds configuration for creating providers.
type ProviderConfig struct {
	Name       string                 `yaml:"name" json:"name"`
	Enabled    bool                   `yaml:"enabled" json:"enabled"`
	Priority   int                    `yaml:"priority" json:"priority"`
	Config     map[string]interface{} `yaml:"config" json:"config"`
	FallbackTo []string               `yaml:"fallback_to" json:"fallback_to"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd $WORKSPACE/Catalogizer/HelixQA && go test ./pkg/vision/cheaper/ -v -count=1`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
cd $WORKSPACE/Catalogizer/HelixQA
git add pkg/vision/cheaper/provider.go pkg/vision/cheaper/provider_test.go
git commit -m "feat(vision/cheaper): add VisionProvider interface and core types"
```

---

### Task 2: Thread-safe provider registry

**Files:**
- Create: `pkg/vision/cheaper/registry.go`
- Create: `pkg/vision/cheaper/registry_test.go`

- [ ] **Step 1: Write failing tests for registry**

```go
// pkg/vision/cheaper/registry_test.go
// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package cheaper

import (
	"context"
	"image"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubProvider is a minimal VisionProvider for testing.
type stubProvider struct {
	name string
}

func (s *stubProvider) Analyze(_ context.Context, _ image.Image, _ string) (*VisionResult, error) {
	return &VisionResult{Text: "stub", Provider: s.name}, nil
}
func (s *stubProvider) Name() string                                  { return s.name }
func (s *stubProvider) HealthCheck(_ context.Context) error           { return nil }
func (s *stubProvider) GetCapabilities() ProviderCapabilities         { return ProviderCapabilities{} }
func (s *stubProvider) GetCostEstimate(_ int, _ int) float64          { return 0 }

func stubFactory(name string) ProviderFactory {
	return func(_ map[string]interface{}) (VisionProvider, error) {
		return &stubProvider{name: name}, nil
	}
}

func TestRegistry_RegisterAndCreate(t *testing.T) {
	reg := NewRegistry()
	reg.Register("test", stubFactory("test"))

	p, err := reg.Create("test", nil)
	require.NoError(t, err)
	assert.Equal(t, "test", p.Name())
}

func TestRegistry_CreateUnknown(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Create("nonexistent", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown provider")
}

func TestRegistry_List(t *testing.T) {
	reg := NewRegistry()
	reg.Register("a", stubFactory("a"))
	reg.Register("b", stubFactory("b"))

	names := reg.List()
	assert.Len(t, names, 2)
	assert.Contains(t, names, "a")
	assert.Contains(t, names, "b")
}

func TestRegistry_IsRegistered(t *testing.T) {
	reg := NewRegistry()
	reg.Register("x", stubFactory("x"))
	assert.True(t, reg.IsRegistered("x"))
	assert.False(t, reg.IsRegistered("y"))
}

func TestRegistry_Unregister(t *testing.T) {
	reg := NewRegistry()
	reg.Register("rm", stubFactory("rm"))
	reg.Unregister("rm")
	assert.False(t, reg.IsRegistered("rm"))
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	reg := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			name := "provider-" + time.Now().String()
			reg.Register(name, stubFactory(name))
			reg.IsRegistered(name)
			reg.List()
		}(i)
	}
	wg.Wait()
}

func TestRegistry_DuplicateRegisterPanics(t *testing.T) {
	reg := NewRegistry()
	reg.Register("dup", stubFactory("dup"))
	assert.Panics(t, func() {
		reg.Register("dup", stubFactory("dup"))
	})
}

func TestRegistry_NilFactoryPanics(t *testing.T) {
	reg := NewRegistry()
	assert.Panics(t, func() {
		reg.Register("nil", nil)
	})
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd $WORKSPACE/Catalogizer/HelixQA && go test ./pkg/vision/cheaper/ -v -run TestRegistry`
Expected: FAIL — NewRegistry not defined

- [ ] **Step 3: Implement registry.go**

```go
// pkg/vision/cheaper/registry.go
// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package cheaper

import (
	"fmt"
	"sync"
)

// Registry manages the mapping from provider names to their factory functions.
// It is safe for concurrent use.
type Registry struct {
	factories map[string]ProviderFactory
	mu        sync.RWMutex
}

// NewRegistry returns an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]ProviderFactory),
	}
}

// Register adds a provider factory. It panics if factory is nil or
// if the name is already registered.
func (r *Registry) Register(name string, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if factory == nil {
		panic("cheaper: Register factory is nil")
	}
	if _, exists := r.factories[name]; exists {
		panic(fmt.Sprintf("cheaper: provider %q already registered", name))
	}
	r.factories[name] = factory
}

// Create instantiates a provider by name using the registered factory.
func (r *Registry) Create(name string, config map[string]interface{}) (VisionProvider, error) {
	r.mu.RLock()
	factory, ok := r.factories[name]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("cheaper: unknown provider %q", name)
	}
	return factory(config)
}

// List returns all registered provider names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}

// IsRegistered checks whether a provider name has been registered.
func (r *Registry) IsRegistered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[name]
	return ok
}

// Unregister removes a provider from the registry. Mainly for testing.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.factories, name)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd $WORKSPACE/Catalogizer/HelixQA && go test ./pkg/vision/cheaper/ -v -count=1 -race`
Expected: PASS (all registry tests including race detector)

- [ ] **Step 5: Commit**

```bash
cd $WORKSPACE/Catalogizer/HelixQA
git add pkg/vision/cheaper/registry.go pkg/vision/cheaper/registry_test.go
git commit -m "feat(vision/cheaper): add thread-safe provider registry"
```

---

## Phase 2: Provider Adapters

### Task 3: Shared image encoding utility

**Files:**
- Create: `pkg/vision/cheaper/adapters/encoding.go`
- Create: `pkg/vision/cheaper/adapters/encoding_test.go`

- [ ] **Step 1: Write failing test**

```go
// pkg/vision/cheaper/adapters/encoding_test.go
// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"image"
	"image/color"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	return img
}

func TestImageToBase64_ValidImage(t *testing.T) {
	img := newTestImage(10, 10)
	b64, err := ImageToBase64(img)
	require.NoError(t, err)
	assert.NotEmpty(t, b64)
	// PNG base64 always starts with iVBOR
	assert.Contains(t, b64[:5], "iVBOR")
}

func TestImageToBase64_SinglePixel(t *testing.T) {
	img := newTestImage(1, 1)
	b64, err := ImageToBase64(img)
	require.NoError(t, err)
	assert.NotEmpty(t, b64)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd $WORKSPACE/Catalogizer/HelixQA && go test ./pkg/vision/cheaper/adapters/ -v`
Expected: FAIL — ImageToBase64 not defined

- [ ] **Step 3: Implement encoding.go**

```go
// pkg/vision/cheaper/adapters/encoding.go
// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package adapters contains shared utilities for vision provider adapters.
package adapters

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
)

// ImageToBase64 encodes an image.Image to a PNG base64 string.
func ImageToBase64(img image.Image) (string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
```

- [ ] **Step 4: Run tests, verify pass**

Run: `cd $WORKSPACE/Catalogizer/HelixQA && go test ./pkg/vision/cheaper/adapters/ -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd $WORKSPACE/Catalogizer/HelixQA
git add pkg/vision/cheaper/adapters/
git commit -m "feat(vision/cheaper): add shared image encoding utility"
```

---

### Task 4: UI-TARS adapter

**Files:**
- Create: `pkg/vision/cheaper/adapters/uitars/uitars.go`
- Create: `pkg/vision/cheaper/adapters/uitars/uitars_test.go`

- [ ] **Step 1: Write failing test with httptest mock**

Test file at `pkg/vision/cheaper/adapters/uitars/uitars_test.go` — tests NewUITARSProvider (missing key error, success), Analyze (successful response, API error, empty choices), HealthCheck (healthy, unhealthy), Name, GetCapabilities, GetCostEstimate. All HTTP calls hit an httptest.Server returning canned JSON.

- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement uitars.go** — OpenAI-compatible chat/completions with vision, HuggingFace inference API, Bearer auth
- [ ] **Step 4: Run tests, verify pass**
- [ ] **Step 5: Commit** — `feat(vision/cheaper): add UI-TARS 1.5-7B adapter`

### Task 5: ShowUI adapter

**Files:**
- Create: `pkg/vision/cheaper/adapters/showui/showui.go`
- Create: `pkg/vision/cheaper/adapters/showui/showui_test.go`

- [ ] **Step 1-5:** Same TDD cycle. Gradio API format (`{"data": [base64, prompt]}` -> `{"data": ["response"]}`), local deployment, no auth.
- [ ] **Commit:** `feat(vision/cheaper): add ShowUI-2B adapter`

### Task 6: GLM-4V adapter

**Files:**
- Create: `pkg/vision/cheaper/adapters/glm4v/glm4v.go`
- Create: `pkg/vision/cheaper/adapters/glm4v/glm4v_test.go`

- [ ] **Step 1-5:** TDD cycle. Zhipu AI API (bigmodel.cn), Bearer auth, glm-4v-flash free tier, note: no `data:image/png;base64,` prefix on image_url for Zhipu.
- [ ] **Commit:** `feat(vision/cheaper): add GLM-4V adapter (Zhipu AI)`

### Task 7: Qwen2.5-VL adapter

**Files:**
- Create: `pkg/vision/cheaper/adapters/qwen25vl/qwen25vl.go`
- Create: `pkg/vision/cheaper/adapters/qwen25vl/qwen25vl_test.go`

- [ ] **Step 1-5:** TDD cycle. Local OpenAI-compatible endpoint (localhost:9192/v1), no auth needed, supports webp+gif.
- [ ] **Commit:** `feat(vision/cheaper): add Qwen2.5-VL adapter`

### Task 8: OmniParser adapter

**Files:**
- Create: `pkg/vision/cheaper/adapters/omniparser/omniparser.go`
- Create: `pkg/vision/cheaper/adapters/omniparser/omniparser_test.go`

- [ ] **Step 1-5:** TDD cycle. Gradio API (like ShowUI), returns structured UI element data (bounding boxes, element types, captions).
- [ ] **Commit:** `feat(vision/cheaper): add OmniParser V2 adapter`

---

## Phase 3: Resilient Executor

### Task 9: Add failsafe-go dependency

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add dependency**

```bash
cd $WORKSPACE/Catalogizer/HelixQA
go get github.com/failsafe-go/failsafe-go@latest
go mod tidy
```

- [ ] **Step 2: Verify builds**

```bash
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add failsafe-go dependency for resilient vision execution"
```

### Task 10: ResilientExecutor with 4 strategies

**Files:**
- Create: `pkg/vision/cheaper/executor.go`
- Create: `pkg/vision/cheaper/executor_test.go`

- [ ] **Step 1: Write failing tests for all 4 strategies**

Tests: TestExecutor_FirstSuccess (first provider succeeds), TestExecutor_FirstSuccess_AllFail, TestExecutor_Parallel (returns fastest), TestExecutor_FallbackChain (first fails, second succeeds), TestExecutor_FallbackChain_Exhausted, TestExecutor_Weighted, TestExecutor_WithRetry (transient error retried), TestExecutor_CircuitBreaker_Opens (after N failures), TestExecutor_Timeout (context deadline). All use stubProvider variants that sleep or return errors.

- [ ] **Step 2: Run tests to verify they fail**
- [ ] **Step 3: Implement executor.go** — `ResilientExecutor` with failsafe-go retry/circuit-breaker/timeout policies, 4 strategy methods, `GetCircuitBreakerState`, `GetProviderStats`
- [ ] **Step 4: Run tests with race detector**

```bash
go test ./pkg/vision/cheaper/ -v -count=1 -race -run TestExecutor
```

- [ ] **Step 5: Commit** — `feat(vision/cheaper): add resilient executor with failsafe-go`

---

## Phase 4: Caching Layer

### Task 11: L1 exact image cache

**Files:**
- Create: `pkg/vision/cheaper/cache/exact.go`
- Create: `pkg/vision/cheaper/cache/exact_test.go`

- [ ] **Step 1: Write failing tests** — TestExactCache_Miss, TestExactCache_Hit, TestExactCache_DifferentPrompt, TestExactCache_Eviction (max size), TestExactCache_ConcurrentAccess
- [ ] **Step 2: Run tests to verify they fail**
- [ ] **Step 3: Implement exact.go** — SHA-256 of image pixels + prompt hash as key, sync.RWMutex, configurable max entries with LRU eviction
- [ ] **Step 4: Run tests, verify pass with -race**
- [ ] **Step 5: Commit** — `feat(vision/cheaper): add L1 exact image cache`

### Task 12: Add go-cache dependency + L2 differential cache

**Files:**
- Modify: `go.mod`
- Create: `pkg/vision/cheaper/cache/differential.go`
- Create: `pkg/vision/cheaper/cache/differential_test.go`

- [ ] **Step 1: Add dependency**

```bash
go get github.com/patrickmn/go-cache@latest
go mod tidy
```

- [ ] **Step 2: Write failing tests** — TestDiffCache_IdenticalFrame (returns cached), TestDiffCache_SmallChange (below threshold returns cached), TestDiffCache_LargeChange (above threshold returns nil), TestDiffCache_NoHistory (returns nil), TestDiffCache_StoreAndRetrieve
- [ ] **Step 3: Implement differential.go** — 24x24 patch hashing, go-cache with 5min TTL, change ratio detection, `GetCachedResponse`, `StoreFrame`
- [ ] **Step 4: Run tests, verify pass**
- [ ] **Step 5: Commit** — `feat(vision/cheaper): add L2 differential cache with patch hashing`

---

## Phase 5: Vector Memory

### Task 13: Add chromem-go dependency

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add dependency**

```bash
go get github.com/philippgille/chromem-go@latest
go mod tidy
```

- [ ] **Step 2: Verify builds**
- [ ] **Step 3: Commit** — `chore: add chromem-go for vector memory`

### Task 14: Image hashing utilities

**Files:**
- Create: `pkg/vision/cheaper/memory/hash.go`
- Create: `pkg/vision/cheaper/memory/hash_test.go`

- [ ] **Step 1: Write failing tests** — TestComputeImageHash_Deterministic (same image = same hash), TestComputeImageHash_DifferentImages (different pixels = different hash), TestComputeImageHash_SinglePixel
- [ ] **Step 2: Run tests to verify they fail**
- [ ] **Step 3: Implement hash.go** — SHA-256 over raw RGBA pixel bytes
- [ ] **Step 4: Run tests, verify pass**
- [ ] **Step 5: Commit** — `feat(vision/cheaper): add image hashing utilities`

### Task 15: Vector memory store

**Files:**
- Create: `pkg/vision/cheaper/memory/vector_store.go`
- Create: `pkg/vision/cheaper/memory/vector_store_test.go`

- [ ] **Step 1: Write failing tests** — TestVectorStore_StoreAndSearch (store memory, search by similar query, verify found), TestVectorStore_GetFewShotExamples (only returns success=true), TestVectorStore_EmptySearch (no results on empty store), TestVectorStore_Persistence (store, create new instance from same path, verify data), TestVectorStore_ConcurrentAccess
- [ ] **Step 2: Run tests to verify they fail**
- [ ] **Step 3: Implement vector_store.go** — `VisionMemory` struct, `VectorMemoryStore` wrapping chromem-go DB, `Store`, `Search`, `GetFewShotExamples`, persistence via `ExportToFile`/`ImportFromFile`
- [ ] **Step 4: Run tests, verify pass with -race**
- [ ] **Step 5: Commit** — `feat(vision/cheaper): add chromem-go vector memory store`

---

## Phase 6: Learning System

### Task 16: Few-shot prompt builder

**Files:**
- Create: `pkg/vision/cheaper/learning/few_shot.go`
- Create: `pkg/vision/cheaper/learning/few_shot_test.go`

- [ ] **Step 1: Write failing tests** — TestFewShotBuilder_NoExamples (returns original prompt), TestFewShotBuilder_WithExamples (augments prompt with examples header), TestFewShotBuilder_BelowMinScore (filters low-score examples), TestFewShotBuilder_LearnFromSuccess (stores and retrieves)
- [ ] **Step 2-5:** TDD cycle, commit as `feat(vision/cheaper): add few-shot prompt builder`

### Task 17: Provider optimizer

**Files:**
- Create: `pkg/vision/cheaper/learning/optimizer.go`
- Create: `pkg/vision/cheaper/learning/optimizer_test.go`

- [ ] **Step 1: Write failing tests** — TestOptimizer_RecordSuccess (increments counters), TestOptimizer_RecordFailure, TestOptimizer_GetBestProvider (returns highest-scoring), TestOptimizer_GetBestProvider_UIType (button vs text accuracy), TestOptimizer_StaleProvider (not used in 10min, skipped), TestOptimizer_ConcurrentAccess
- [ ] **Step 2-5:** TDD cycle, commit as `feat(vision/cheaper): add provider optimizer with UI-type tracking`

### Task 18: Learning vision executor (5-layer pipeline)

**Files:**
- Create: `pkg/vision/cheaper/learning/executor.go`
- Create: `pkg/vision/cheaper/learning/executor_test.go`

- [ ] **Step 1: Write failing tests** — TestLearningExecutor_L1_ExactCacheHit, TestLearningExecutor_L2_DiffCacheHit, TestLearningExecutor_L3_VectorMemoryHit, TestLearningExecutor_L4_FewShotAugments, TestLearningExecutor_L5_FallsThrough (all layers miss, calls resilient executor), TestLearningExecutor_PostExecution_Learns (verify async store after execute), TestLearningExecutor_DisabledLayers (config disables layers)
- [ ] **Step 2-5:** TDD cycle, commit as `feat(vision/cheaper): add 5-layer learning vision executor`

---

## Phase 7: Integration Bridge + Metrics

### Task 19: Bridge adapter (VisionProvider -> pkg/llm.Provider)

**Files:**
- Create: `pkg/vision/cheaper/bridge.go`
- Create: `pkg/vision/cheaper/bridge_test.go`

- [ ] **Step 1: Write failing tests** — TestBridge_Chat (returns error, not supported), TestBridge_Vision (converts []byte to image.Image, calls Analyze), TestBridge_Name, TestBridge_SupportsVision (true)
- [ ] **Step 2-5:** TDD cycle. The bridge decodes `[]byte` (PNG) to `image.Image`, calls `VisionProvider.Analyze`, converts `VisionResult` to `llm.Response`. Commit as `feat(vision/cheaper): add bridge adapter to pkg/llm.Provider`

### Task 20: Prometheus metrics

**Files:**
- Create: `pkg/vision/cheaper/metrics.go`
- Create: `pkg/vision/cheaper/metrics_test.go`

- [ ] **Step 1: Write failing tests** — TestMetrics_RequestTotal (increments), TestMetrics_CacheHits (by layer), TestMetrics_RequestDuration (observes), TestMetrics_CircuitBreakerState (gauge)
- [ ] **Step 2-5:** TDD cycle. Prometheus counters: `cheaper_vision_requests_total{provider}`, `cheaper_vision_cache_hits_total{layer}`, histograms: `cheaper_vision_request_duration_seconds{provider}`, gauges: `cheaper_vision_circuit_breaker_state{provider}`. Commit as `feat(vision/cheaper): add Prometheus metrics`

---

## Phase 8: Vision Server

### Task 21: Configuration loader

**Files:**
- Create: `internal/visionserver/config.go`
- Create: `internal/visionserver/config_test.go`

- [ ] **Step 1: Write failing tests** — TestConfig_LoadFromEnv (reads HELIX_VISION_* vars), TestConfig_Defaults (all defaults populated), TestConfig_ProviderList (parses comma-separated fallback chain)
- [ ] **Step 2-5:** TDD cycle. Reads env vars listed in DOCUMENTATION.md (HELIX_VISION_PROVIDER, HELIX_VISION_TIMEOUT, etc.). Commit as `feat(visionserver): add configuration loader`

### Task 22: HTTP handlers + server

**Files:**
- Create: `internal/visionserver/handlers.go`
- Create: `internal/visionserver/server.go`
- Create: `internal/visionserver/handlers_test.go`
- Create: `internal/visionserver/server_test.go`

- [ ] **Step 1: Write failing tests** — TestHandler_Analyze (POST /api/v1/vision/analyze with base64 image returns result), TestHandler_Analyze_InvalidImage, TestHandler_ListProviders, TestHandler_Health (returns status+providers), TestHandler_Metrics (Prometheus format), TestServer_StartStop
- [ ] **Step 2-5:** TDD cycle. REST API: POST /api/v1/vision/analyze, GET /api/v1/providers, GET /health, GET /metrics, POST /api/v1/providers/{name}/enable, POST /api/v1/providers/{name}/disable, GET /api/v1/learning/stats, POST /api/v1/learning/clear. Commit as `feat(visionserver): add HTTP server with vision API`

---

## Phase 9: Wire Into Existing Pipeline

### Task 23: Register cheaper providers in vision ranking

**Files:**
- Modify: `pkg/llm/vision_ranking.go`

- [ ] **Step 1: Add entries for cheaper providers**

Add to `visionModelRegistry`:
```go
// Tier 3: Cheaper / self-hosted vision models
{Provider: "uitars",   QualityScore: 0.82, ReliabilityScore: 0.75, CostPer1kTokens: 0.0,   AvgLatencyMs: 2000},
{Provider: "showui",   QualityScore: 0.70, ReliabilityScore: 0.70, CostPer1kTokens: 0.0,   AvgLatencyMs: 500},
{Provider: "glm4v",    QualityScore: 0.78, ReliabilityScore: 0.80, CostPer1kTokens: 0.0,   AvgLatencyMs: 1000},
{Provider: "qwen25vl", QualityScore: 0.87, ReliabilityScore: 0.85, CostPer1kTokens: 0.0,   AvgLatencyMs: 3000},
```

- [ ] **Step 2: Run existing tests to verify no regression**

```bash
go test ./pkg/llm/ -v -count=1 -race
```

- [ ] **Step 3: Commit** — `feat(llm): register cheaper vision providers in ranking`

### Task 24: Wire learning executor into autonomous pipeline

**Files:**
- Modify: `pkg/autonomous/pipeline.go`

- [ ] **Step 1: Add optional cheaper vision initialization** — When `HELIX_VISION_CHEAPER_ENABLED=true`, create a `LearningVisionExecutor` with configured providers, wrap it in a `BridgeProvider`, and prepend it to the adaptive provider's provider list.
- [ ] **Step 2: Run existing pipeline tests**
- [ ] **Step 3: Commit** — `feat(autonomous): wire cheaper vision providers into pipeline`

---

## Phase 10: Comprehensive Testing

### Task 25: Integration tests

**Files:**
- Create: `pkg/vision/cheaper/integration_test.go`

- [ ] **Step 1: Write integration tests** — TestIntegration_FullPipeline (registry -> create providers -> executor -> learning executor -> analyze), TestIntegration_CacheLayers (L1 hit after first call, L2 hit on similar frame), TestIntegration_FewShot_Improves (store examples, verify augmented prompt)
- [ ] **Step 2-3:** Run and verify
- [ ] **Step 4: Commit** — `test(vision/cheaper): add integration tests for full pipeline`

### Task 26: Benchmark tests

**Files:**
- Create: `pkg/vision/cheaper/benchmark_test.go`

- [ ] **Step 1: Write benchmarks** — BenchmarkExactCache_Hit, BenchmarkExactCache_Miss, BenchmarkDiffCache_IdenticalFrame, BenchmarkRegistry_Create, BenchmarkImageToBase64, BenchmarkComputeImageHash
- [ ] **Step 2: Run and record baselines**

```bash
go test ./pkg/vision/cheaper/... -bench=. -benchmem -count=3
```

- [ ] **Step 3: Commit** — `test(vision/cheaper): add benchmark tests`

### Task 27: Fuzz tests

**Files:**
- Create: `pkg/vision/cheaper/fuzz_test.go`

- [ ] **Step 1: Write fuzz tests** — FuzzImageHash (random pixel data), FuzzPromptHash (random strings), FuzzDifferentialCache_ChangeDetection (random images)
- [ ] **Step 2: Run briefly**

```bash
go test ./pkg/vision/cheaper/ -fuzz=FuzzImageHash -fuzztime=30s
```

- [ ] **Step 3: Commit** — `test(vision/cheaper): add fuzz tests`

### Task 28: Concurrency + memory leak tests

**Files:**
- Create: `pkg/vision/cheaper/concurrency_test.go`

- [ ] **Step 1: Write concurrency tests** — TestConcurrency_ExecutorParallel (100 goroutines hitting executor), TestConcurrency_RegistryHotPath, TestConcurrency_CacheContention, TestConcurrency_VectorStoreParallel. All run with `-race`.
- [ ] **Step 2: Run with race detector**
- [ ] **Step 3: Commit** — `test(vision/cheaper): add concurrency and leak tests`

---

## Phase 11: Documentation

### Task 29: User guide

**Files:**
- Create: `docs/vision/USER_GUIDE.md`

- [ ] **Step 1: Write user guide** covering: Quick Start (install, configure, run), Configuration Options (all env vars), Provider Setup (each provider with YAML examples), Execution Strategies (first_success, parallel, fallback), Learning System (5 layers explained), Monitoring (Prometheus metrics, Grafana dashboard)
- [ ] **Step 2: Commit** — `docs(vision): add comprehensive user guide`

### Task 30: Admin guide

**Files:**
- Create: `docs/vision/ADMIN_GUIDE.md`

- [ ] **Step 1: Write admin guide** covering: Deployment (Podman, bare metal), Scaling (horizontal, load balancing), Backup/Recovery (vector memory), Security (API keys, network isolation, TLS), Performance Tuning (cache TTL, circuit breaker, provider weights), Troubleshooting (common issues table)
- [ ] **Step 2: Commit** — `docs(vision): add administrator guide`

### Task 31: API reference

**Files:**
- Create: `docs/vision/API_REFERENCE.md`

- [ ] **Step 1: Write API reference** covering: All REST endpoints with request/response JSON examples, error codes, authentication
- [ ] **Step 2: Commit** — `docs(vision): add API reference`

---

## Phase 12: Final Verification + Push

### Task 32: Full test suite run

- [ ] **Step 1: Run all tests with race detector**

```bash
cd $WORKSPACE/Catalogizer/HelixQA
go test ./pkg/vision/cheaper/... -v -count=1 -race
go test ./internal/visionserver/... -v -count=1 -race
go vet ./pkg/vision/cheaper/... ./internal/visionserver/...
```

- [ ] **Step 2: Run full HelixQA test suite to verify no regressions**

```bash
go test ./... -count=1 -race -p 2 -parallel 2
```

- [ ] **Step 3: Verify zero warnings**

```bash
go vet ./...
```

### Task 33: Commit all + push submodules and main repo

- [ ] **Step 1: Final commit if needed**
- [ ] **Step 2: Push HelixQA to all remotes**

```bash
cd $WORKSPACE/Catalogizer/HelixQA
for remote in $(git remote); do
  GIT_SSH_COMMAND="ssh -o BatchMode=yes" git push $remote main 2>&1
done
```

- [ ] **Step 3: Update and push main repo**

```bash
cd $WORKSPACE/Catalogizer
git add HelixQA
git commit -m "chore: update HelixQA submodule — cheaper vision integration"
GIT_SSH_COMMAND="ssh -o BatchMode=yes" git push origin main
```

---

## Summary

| Phase | Tasks | New Files | Test Files |
|-------|-------|-----------|------------|
| 1: Core Interfaces | 1-2 | 2 | 2 |
| 2: Provider Adapters | 3-8 | 12 | 6 |
| 3: Resilient Executor | 9-10 | 1 | 1 |
| 4: Caching Layer | 11-12 | 2 | 2 |
| 5: Vector Memory | 13-15 | 2 | 2 |
| 6: Learning System | 16-18 | 3 | 3 |
| 7: Bridge + Metrics | 19-20 | 2 | 2 |
| 8: Vision Server | 21-22 | 4 | 4 |
| 9: Pipeline Wiring | 23-24 | 0 (modify) | 0 |
| 10: Testing | 25-28 | 4 | 0 |
| 11: Documentation | 29-31 | 3 | 0 |
| 12: Verification | 32-33 | 0 | 0 |
| **Total** | **33** | **~35** | **~22** |
