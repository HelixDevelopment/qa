# Configuration Reference

HelixQA is configured through environment variables, `.env` files, and YAML config fields. This page is the exhaustive reference for every configuration option.

## Configuration Precedence

Variables are resolved in this order (highest to lowest priority):

1. **Shell environment** -- exported variables in the current session
2. **`--env` file** -- specified on the CLI via `--env path/to/.env`
3. **`.env` in the working directory** -- auto-loaded if present
4. **Built-in defaults** -- hardcoded in `pkg/config/config.go`

## Environment File

Create a `.env` file and pass it with the `--env` flag:

```bash
helixqa autonomous --project . --platforms all --env .env
```

The file uses standard `KEY=VALUE` format, one variable per line. Lines starting with `#` are comments.

```env
# LLM provider (at least one required for autonomous mode)
ANTHROPIC_API_KEY=sk-ant-...

# Target platform configuration
HELIX_WEB_URL=http://localhost:3000
HELIX_ANDROID_DEVICE=192.0.2.214:5555
HELIX_ANDROID_PACKAGE=com.example.myapp

# Optional tuning
HELIX_FFMPEG_PATH=/usr/bin/ffmpeg
HELIX_DESKTOP_DISPLAY=:0
```

---

## LLM Provider Variables

Set at least one provider key for autonomous mode. Multiple providers can be active simultaneously -- the `AdaptiveProvider` selects the best one per request type (reasoning vs. vision).

### Tier 1 -- Primary Providers

| Variable | Provider | Default Model | Notes |
|----------|----------|---------------|-------|
| `ANTHROPIC_API_KEY` | Anthropic Claude | claude-sonnet-4 | Best vision analysis quality |
| `OPENAI_API_KEY` | OpenAI GPT | gpt-4o | Strong all-round |
| `OPENROUTER_API_KEY` | OpenRouter | anthropic/claude-sonnet-4 | 100+ models via one key |
| `DEEPSEEK_API_KEY` | DeepSeek | deepseek-chat | Most cost-effective |
| `GROQ_API_KEY` | Groq | llama-3.3-70b-versatile | Fastest inference |
| `HELIX_OLLAMA_URL` | Ollama (self-hosted) | (your local model) | No cost, air-gapped |

### Tier 2 -- OpenAI-Compatible Providers

| Variable | Provider | Default Model |
|----------|----------|---------------|
| `AI21_API_KEY` | AI21 | jamba-1.5-mini |
| `CEREBRAS_API_KEY` | Cerebras | llama-3.3-70b |
| `CHUTES_API_KEY` | Chutes | deepseek-chat |
| `CLOUDFLARE_API_KEY` | Cloudflare Workers AI | (per account) |
| `CODESTRAL_API_KEY` | Codestral | codestral-latest |
| `COHERE_API_KEY` | Cohere | command-r-plus |
| `FIREWORKS_API_KEY` | Fireworks AI | llama-v3p3-70b-instruct |
| `GITHUB_MODELS_API_KEY` | GitHub Models | openai/gpt-4o |
| `HUGGINGFACE_API_KEY` | HuggingFace | (model-dependent) |
| `HYPERBOLIC_API_KEY` | Hyperbolic | deepseek-ai/DeepSeek-V3 |
| `KIMI_API_KEY` | Kimi (Moonshot) | moonshot-v1-8k |
| `MISTRAL_API_KEY` | Mistral | mistral-large-latest |
| `NVIDIA_API_KEY` | NVIDIA NIM | meta/llama-3.3-70b-instruct |
| `PERPLEXITY_API_KEY` | Perplexity | sonar |
| `QWEN_API_KEY` | Qwen (Alibaba) | qwen-plus |
| `SAMBANOVA_API_KEY` | SambaNova | Meta-Llama-3.3-70B-Instruct |
| `SILICONFLOW_API_KEY` | SiliconFlow | deepseek-ai/DeepSeek-V3 |
| `TOGETHER_API_KEY` | Together AI | Llama-3.3-70B-Instruct-Turbo |
| `VENICE_API_KEY` | Venice | (provider default) |
| `XAI_API_KEY` | xAI (Grok) | grok-3 |

See [LLM Providers](/providers) for the complete list of 40+ supported providers.

---

## Device and Platform Variables

| Variable | Purpose | Example |
|----------|---------|---------|
| `HELIX_ANDROID_DEVICE` | ADB device or emulator ID | `192.0.2.214:5555` |
| `HELIX_ANDROID_PACKAGE` | Android application package name | `com.example.myapp` |
| `HELIX_WEB_URL` | Web application base URL | `http://localhost:3000` |
| `HELIX_DESKTOP_DISPLAY` | X11 display for desktop executor | `:0` |
| `HELIX_API_URL` | Base URL for REST API executor | `http://localhost:8080` |
| `HELIX_API_TOKEN` | Bearer token for API executor auth | `eyJhbGci...` |
| `HELIX_FFMPEG_PATH` | Path to the ffmpeg binary | `/usr/bin/ffmpeg` |

---

## Model Override Variables

| Variable | Purpose | Example |
|----------|---------|---------|
| `HELIX_LLM_MODEL` | Override default model for the selected provider | `gpt-4o-mini` |
| `HELIX_VISION_MODEL` | Override model used for screenshot analysis | `claude-opus-4` |
| `HELIX_OLLAMA_MODEL` | Model identifier for Ollama provider | `llama3.3` |

---

## Memory and Storage Variables

| Variable | Purpose | Default |
|----------|---------|---------|
| `HELIX_MEMORY_DB` | Path to SQLite memory database | `helix_qa/data/memory.db` |
| `HELIX_OUTPUT_DIR` | Default output directory | `qa-results` |
| `HELIX_ISSUES_DIR` | Directory for generated issue tickets | `docs/issues` |

---

## Autonomous Config Fields

When using HelixQA as a library or configuring via YAML, the `AutonomousConfig` struct provides fine-grained control. These fields map to the `autonomous` section.

### Agent Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `agents_enabled` | `[]string` | `["opencode", "claude-code", "gemini"]` | CLI agents to use for execution |
| `agent_pool_size` | `int` | `3` | Number of agents in the pool |
| `agent_timeout` | `duration` | `60s` | Timeout per agent operation |
| `agent_max_retries` | `int` | `3` | Maximum retries per LLM call |

### Vision Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `vision_provider` | `string` | `auto` | Vision provider: `auto`, `openai`, `anthropic`, `gemini`, `qwen` |
| `vision_opencv_enabled` | `bool` | `true` | Enable OpenCV-based analysis alongside LLM vision |
| `vision_ssim_threshold` | `float64` | `0.95` | SSIM similarity threshold for duplicate detection |

### Documentation Ingestion

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `docs_root` | `string` | `./docs` | Path to project documentation root |
| `docs_auto_discover` | `bool` | `true` | Enable automatic documentation discovery |
| `docs_formats` | `[]string` | `["md", "yaml", "html", "adoc", "rst"]` | Supported documentation file formats |

### Recording Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `recording_video` | `bool` | `true` | Enable video recording |
| `recording_screenshots` | `bool` | `true` | Enable screenshot capture |
| `recording_video_quality` | `string` | `medium` | Video quality: `low`, `medium`, `high` |
| `recording_screenshot_format` | `string` | `png` | Screenshot format: `png`, `jpg` |
| `recording_audio` | `bool` | `false` | Enable audio recording |
| `recording_audio_quality` | `string` | `high` | Audio quality: `standard` (44.1kHz/16bit), `high` (48kHz/24bit), `ultra` (96kHz/32bit) |
| `recording_audio_format` | `string` | `wav` | Audio format: `wav` (lossless), `flac` (lossless compressed) |
| `recording_audio_device` | `string` | `default` | Audio input device; `default` for system, `adb` for Android device |
| `recording_ffmpeg_path` | `string` | `/usr/bin/ffmpeg` | Path to ffmpeg binary |

### Ticket Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `tickets_enabled` | `bool` | `true` | Generate markdown issue tickets |
| `tickets_min_severity` | `string` | `low` | Minimum severity to generate a ticket: `critical`, `high`, `medium`, `low`, `cosmetic` |

### LLM Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `llm_provider` | `string` | -- | Preferred LLM provider name; blank for auto-selection |
| `llm_api_key` | `string` | -- | API key (overridden by provider-specific env vars) |
| `llm_base_url` | `string` | -- | Base URL for self-hosted providers like Ollama |
| `llm_model` | `string` | -- | Model identifier (overridden by `HELIX_OLLAMA_MODEL`) |

### Memory and Output

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `memory_db_path` | `string` | `<project>/helix_qa/data/memory.db` | SQLite memory store path |
| `issues_dir` | `string` | `docs/issues` | Issue ticket output directory |

---

## Per-Platform Minimum Configuration

### Web Only

```env
OPENROUTER_API_KEY=sk-or-v1-...
HELIX_WEB_URL=http://localhost:3000
```

### Android Only

```env
ANTHROPIC_API_KEY=sk-ant-...
HELIX_ANDROID_DEVICE=192.0.2.214:5555
HELIX_ANDROID_PACKAGE=com.example.myapp
```

### Full Stack (Android + Web + Desktop)

```env
ANTHROPIC_API_KEY=sk-ant-...
HELIX_WEB_URL=http://localhost:3000
HELIX_ANDROID_DEVICE=192.0.2.214:5555
HELIX_ANDROID_PACKAGE=com.example.myapp
HELIX_DESKTOP_DISPLAY=:0
HELIX_FFMPEG_PATH=/usr/bin/ffmpeg
```

### Fully Self-Hosted (No Cloud APIs)

```env
HELIX_OLLAMA_URL=http://localhost:11434
HELIX_OLLAMA_MODEL=llama3.3
HELIX_WEB_URL=http://localhost:3000
```

---

## LLM Provider Selection Strategy

When multiple provider keys are set, the `AdaptiveProvider` routes requests:

1. **Vision requests** (screenshot analysis) route to providers with multimodal capability (Anthropic, OpenAI, Ollama with llava)
2. **Reasoning requests** (planning, test generation) route to the fastest available provider
3. **Fallback** cascades to the next available provider on rate limit or error
4. **Health tracking** avoids recently-failed endpoints

Override automatic selection by setting `HELIX_LLM_MODEL` or the `llm_provider` config field.

## Related Pages

- [CLI Reference](/reference/cli) -- all command flags
- [LLM Providers](/providers) -- full provider reference with default models
- [Installation](/installation) -- initial setup and prerequisites
- [Test Bank Schema](/reference/test-bank-schema) -- YAML test bank format
