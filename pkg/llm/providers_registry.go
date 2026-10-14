// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

// providerDefaults maps provider names to their default base URL
// and model. All providers listed here use the OpenAI-compatible
// chat/completions API and are constructed via NewOpenAIProvider.
var providerDefaults = map[string]struct {
	BaseURL string
	Model   string
}{
	ProviderOpenRouter: {"https://openrouter.ai/api", "anthropic/claude-sonnet-4"},
	ProviderDeepSeek:   {"https://api.deepseek.com", "deepseek-chat"},
	ProviderGroq:       {"https://api.groq.com/openai", "llama-3.3-70b-versatile"},
	"ai21":             {"https://api.ai21.com/studio", "jamba-1.5-mini"},
	"cerebras":         {"https://api.cerebras.ai", "llama-3.3-70b"},
	"chutes":           {"https://llm.chutes.ai", "deepseek-chat"},
	"cloudflare":       {"https://api.cloudflare.com/client/v4/accounts/default/ai", ""},
	"codestral":        {"https://api.mistral.ai", "codestral-latest"},
	"fireworks":        {"https://api.fireworks.ai/inference", "accounts/fireworks/models/llama-v3p3-70b-instruct"},
	"githubmodels":     {"https://models.github.ai/inference", "openai/gpt-4o"},
	"huggingface":      {"https://router.huggingface.co", ""},
	"hyperbolic":       {"https://api.hyperbolic.xyz", "deepseek-ai/DeepSeek-V3"},
	"kimi":             {"https://api.moonshot.cn/v1", "kimi-k2.5"},
	"mistral":          {"https://api.mistral.ai", "mistral-large-latest"},
	"modal":            {"https://api.modal.com", ""},
	"nia":              {"https://api.nia.ai", ""},
	"nlpcloud":         {"https://api.nlpcloud.io/v1/gpu", ""},
	"novita":           {"https://api.novita.ai/v3/openai", ""},
	"nvidia":           {"https://integrate.api.nvidia.com", "meta/llama-3.2-90b-vision-instruct"},
	"perplexity":       {"https://api.perplexity.ai", "sonar"},
	"publicai":         {"https://api.publicai.co", ""},
	"qwen":             {"https://dashscope.aliyuncs.com/compatible-mode", "qwen-vl-max"},
	"replicate":        {"https://api.replicate.com", ""},
	"sambanova":        {"https://api.sambanova.ai", "Meta-Llama-3.3-70B-Instruct"},
	"sarvam":           {"https://api.sarvam.ai", ""},
	"siliconflow":      {"https://api.siliconflow.cn", "deepseek-ai/DeepSeek-V3"},
	"together":         {"https://api.together.xyz", "meta-llama/Llama-3.3-70B-Instruct-Turbo"},
	"upstage":          {"https://api.upstage.ai", "solar-pro"},
	"venice":           {"https://api.venice.ai/api", ""},
	"vulavula":         {"https://api.vulavula.ai", ""},
	"xai":              {"https://api.x.ai", "grok-3"},
	"zai":              {"https://open.bigmodel.cn/api/paas/v4", "glm-4.5"},
	"zen":              {"https://opencode.ai/zen", ""},
	"zhipu":            {"https://open.bigmodel.cn/api/paas/v4", "glm-4.5"},
	"cohere":           {"https://api.cohere.com", "command-r-plus"},
	"stepfun":          {"https://api.stepfun.com/v1", "step-1.5v-mini"},
}

// ProviderEnvKeys maps provider names to their expected
// environment variable names for API keys.
var ProviderEnvKeys = map[string]string{
	ProviderAnthropic:  "ANTHROPIC_API_KEY",
	ProviderOpenAI:     "OPENAI_API_KEY",
	ProviderGoogle:     "GEMINI_API_KEY",
	ProviderOpenRouter: "OPENROUTER_API_KEY",
	ProviderDeepSeek:   "DEEPSEEK_API_KEY",
	ProviderGroq:       "GROQ_API_KEY",
	"ai21":             "AI21_API_KEY",
	"cerebras":         "CEREBRAS_API_KEY",
	"chutes":           "CHUTES_API_KEY",
	"cloudflare":       "CLOUDFLARE_API_KEY",
	"codestral":        "CODESTRAL_API_KEY",
	"cohere":           "COHERE_API_KEY",
	"fireworks":        "FIREWORKS_API_KEY",
	"githubmodels":     "GITHUB_MODELS_API_KEY",
	"huggingface":      "HUGGINGFACE_API_KEY",
	"hyperbolic":       "HYPERBOLIC_API_KEY",
	"kimi":             "KIMI_API_KEY",
	"mistral":          "MISTRAL_API_KEY",
	"modal":            "MODAL_API_KEY",
	"nia":              "NIA_API_KEY",
	"nlpcloud":         "NLPCLOUD_API_KEY",
	"novita":           "NOVITA_API_KEY",
	"nvidia":           "NVIDIA_API_KEY",
	"perplexity":       "PERPLEXITY_API_KEY",
	"publicai":         "PUBLICAI_API_KEY",
	"qwen":             "QWEN_API_KEY",
	"replicate":        "REPLICATE_API_KEY",
	"sambanova":        "SAMBANOVA_API_KEY",
	"sarvam":           "SARVAM_API_KEY",
	"siliconflow":      "SILICONFLOW_API_KEY",
	"together":         "TOGETHER_API_KEY",
	"upstage":          "UPSTAGE_API_KEY",
	"venice":           "VENICE_API_KEY",
	"vulavula":         "VULAVULA_API_KEY",
	"xai":              "XAI_API_KEY",
	"zai":              "ZAI_API_KEY",
	"zen":              "ZEN_API_KEY",
	"zhipu":            "ZHIPU_API_KEY",
	ProviderOllama:     "HELIX_OLLAMA_URL",
	ProviderLlmctl:     "LLMCTL_VISION_URL",
	"stepfun":          "STEPFUN_API_KEY",
	"astica":           "ASTICA_API_KEY",
}

// IsOpenAICompatible returns true if the provider uses the
// OpenAI chat/completions API format.
func IsOpenAICompatible(name string) bool {
	if name == ProviderAnthropic || name == ProviderOllama ||
		name == ProviderUITars || name == ProviderGoogle {
		return false
	}
	_, ok := providerDefaults[name]
	return ok || name == ProviderOpenAI
}
