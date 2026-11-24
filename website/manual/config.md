# Configuration

HelixQA is configured entirely through environment variables. There are no required config files — set variables in your shell, in a `.env` file, or in your container environment.

## Environment File

Create a `.env` file and pass it with `--env`:

```bash
helixqa autonomous --project . --platforms all --env .env
```

Example `.env`:

```env
# LLM provider
ANTHROPIC_API_KEY=sk-ant-...

# Target platforms
HELIX_WEB_URL=http://localhost:3000
HELIX_ANDROID_DEVICE=192.0.2.214:5555
HELIX_ANDROID_PACKAGE=com.example.myapp

# Optional tuning
HELIX_FFMPEG_PATH=/usr/bin/ffmpeg
HELIX_DESKTOP_DISPLAY=:0
```

## LLM Provider Variables

Set at least one. Multiple providers can be active simultaneously — HelixQA selects the best one per request.

| Variable | Provider | Notes |
|----------|----------|-------|
| `ANTHROPIC_API_KEY` | Anthropic Claude | Best vision analysis quality |
| `OPENAI_API_KEY` | OpenAI GPT | Strong all-round |
| `OPENROUTER_API_KEY` | OpenRouter | Access to 100+ models via one key |
| `DEEPSEEK_API_KEY` | DeepSeek | Most cost-effective |
| `GROQ_API_KEY` | Groq | Fastest inference |
| `CEREBRAS_API_KEY` | Cerebras | Fast inference |
| `MISTRAL_API_KEY` | Mistral | European provider |
| `NVIDIA_API_KEY` | NVIDIA NIM | GPU-accelerated inference |
| `FIREWORKS_API_KEY` | Fireworks AI | Fast open-source models |
| `TOGETHER_API_KEY` | Together AI | Open-source model hosting |
| `COHERE_API_KEY` | Cohere | Enterprise NLP |
| `XAI_API_KEY` | xAI Grok | Grok models |
| `KIMI_API_KEY` | Kimi (Moonshot) | Chinese provider |
| `PERPLEXITY_API_KEY` | Perplexity | Search-augmented |
| `HUGGINGFACE_API_KEY` | HuggingFace | Open-source models |
| `SAMBANOVA_API_KEY` | SambaNova | High-throughput inference |
| `SILICONFLOW_API_KEY` | SiliconFlow | Chinese provider |
| `HYPERBOLIC_API_KEY` | Hyperbolic | Open-source models |
| `VENICE_API_KEY` | Venice | Privacy-focused |
| `HELIX_OLLAMA_URL` | Ollama | Self-hosted, e.g. `http://localhost:11434` |
| + 15 more | See [LLM Providers](/providers) | — |

## Device and Platform Variables

| Variable | Purpose | Example |
|----------|---------|---------|
| `HELIX_ANDROID_DEVICE` | ADB device ID | `192.0.2.214:5555` |
| `HELIX_ANDROID_PACKAGE` | Android app package name | `com.example.myapp` |
| `HELIX_WEB_URL` | Web application base URL | `http://localhost:3000` |
| `HELIX_DESKTOP_DISPLAY` | X11 display for desktop executor | `:0` |
| `HELIX_API_URL` | Base URL for REST API executor | `http://localhost:8080` |
| `HELIX_API_TOKEN` | Bearer token for API executor | `eyJhbGci...` |
| `HELIX_FFMPEG_PATH` | Path to ffmpeg binary | `/usr/bin/ffmpeg` |

## Model Override Variables

| Variable | Purpose | Example |
|----------|---------|---------|
| `HELIX_LLM_MODEL` | Override default model for selected provider | `gpt-4o-mini` |
| `HELIX_VISION_MODEL` | Override model used for screenshot analysis | `claude-opus-4` |

## Memory and Storage Variables

| Variable | Purpose | Default |
|----------|---------|---------|
| `HELIX_MEMORY_DB` | Path to SQLite memory database | `helix_qa/data/memory.db` |
| `HELIX_OUTPUT_DIR` | Default output directory | `qa-results` |
| `HELIX_ISSUES_DIR` | Directory for generated issue tickets | `docs/issues` |

## Precedence

Variables are resolved in this order (highest to lowest priority):

1. Shell environment (exported variables)
2. `--env` file specified on the CLI
3. `.env` in the current working directory
4. Built-in defaults

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
HELIX_WEB_URL=http://localhost:3000
```

## Related Pages

- [Installation](/installation) — initial setup and prerequisites
- [CLI Reference](/manual/cli) — all command flags
- [LLM Providers](/providers) — full provider reference with default models
