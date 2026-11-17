# GPT-6.2 Sol / Astra

The gateway preserves `gpt-6.2-sol` and `gpt-6.2-astra` as distinct upstream model IDs. Existing GPT-6 and GPT-6.1 aliases keep their current targets. Operators add the two exact IDs to the paid account templates through the native Admin API; Free/Go entitlements are unchanged. Existing isolation, cooldown and required-route restrictions also protect the corresponding 6.2 model without turning old quality results into new test evidence.

Both models accept text and images, with a 1,050,000-token context, at most 922,000 input tokens and 128,000 output tokens. Use Responses for tools. Chat Completions and Messages requests are translated to Responses on capable routes; a configured Chat-only route rejects tool requests before forwarding. Legacy `none` / `minimal` reasoning is normalized to `low`. The local catalog advertises `low`, `medium`, `high`, `xhigh`, and `max`; upstream metadata can supply additional capabilities when available. Unsupported sampling/logprob parameters are omitted, and legacy cache retention becomes `prompt_cache_options.ttl = "30m"`.

Prices below are USD per million tokens before configured account/group/user multipliers.

| Model | Input | Cached input | 30m cache write | 12h cache write | Output |
|---|---:|---:|---:|---:|---:|
| GPT-6.2 Sol | 2 | 0.10 | 2.50 | 5 | 10 |
| GPT-6.2 Astra | 8 | 0.40 | 10 | 20 | 40 |

Above 272,000 input tokens, the full request uses 2x input/cache rates and 1.5x output rates. Fast uses 2x Standard. Ultrafast uses 3x through February 28, 2027, and 6x from March 1, 2027, Pacific time; request pricing time owns this boundary. Operator price cards retain their explicit cache-write prices. Official-equivalent account statistics use the same published rates without rewriting historical charges.

The model picker and CCS/Magpie imports use the selected Key's actual catalog. New default imports prefer 6.2 Sol when available and preserve an explicit saved selection. These changes do not alter existing installed client configurations.

Sources, checked November 17, 2026: [migration guide](https://developers.openai.com/api/docs/guides/latest-model), [Sol](https://developers.openai.com/api/docs/models/gpt-6.2-sol), [Astra](https://developers.openai.com/api/docs/models/gpt-6.2-astra).
