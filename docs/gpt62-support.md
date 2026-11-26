# GPT-6.2 Sol / Astra

The gateway preserves `gpt-6.2-sol` and `gpt-6.2-astra` as distinct upstream model IDs. Existing GPT-6 and GPT-6.1 aliases keep their current targets. Operators add the two exact IDs to the paid account templates through the native Admin API; Free/Go entitlements are unchanged. Existing isolation, cooldown and required-route restrictions also protect the corresponding 6.2 model without turning old quality results into new test evidence.

The bundled Codex catalog advertises a default context window of 500,000 tokens for Sol and 272,000 for Astra, with a maximum of 872,000 for both. Clients can discover account-specific capabilities from the model catalog. The separate 272,000-token billing threshold is described below. Use Responses for tools. Chat Completions and Messages requests are translated to Responses on capable routes. A configured Chat-only route rejects tool requests before forwarding. Legacy `none` and `minimal` reasoning are normalized to `low`. The Codex catalog can advertise the `ultra` client workflow. The raw API accepts `low`, `medium`, `high`, `xhigh`, and `max`. Unsupported sampling/logprob parameters are omitted, and legacy cache retention becomes `prompt_cache_options.ttl = "30m"`.

Prices below are USD per million tokens before configured account/group/user multipliers.

| Model | Input | Cached input | 30m cache write | 12h cache write | Output |
|---|---:|---:|---:|---:|---:|
| GPT-6.2 Sol | 2 | 0.10 | 2.50 | 5 | 10 |
| GPT-6.2 Astra | 8 | 0.40 | 10 | 20 | 40 |

For an OpenAI API-key account with model long-context pricing enabled, more than 272,000 input tokens applies 2x input/cache rates and 1.5x output rates to the full request. OpenAI OAuth and SetupToken accounts are exempt from this model surcharge when the final upstream model is GPT-6.2 Sol or Astra. Explicit channel pricing intervals still apply. Fast uses 2x Standard. Ultrafast uses 3x through February 28, 2027, and 6x from March 1, 2027, Pacific time. The request pricing time selects the rate. Operator price cards retain their explicit cache-write prices. Official-equivalent account statistics use the published rates without rewriting historical charges.

The model picker and CCS/Magpie imports use the selected Key's actual catalog. New default imports prefer 6.2 Sol when available and preserve an explicit saved selection. These changes do not alter existing installed client configurations.

Sources, checked November 17, 2026: [migration guide](https://developers.openai.com/api/docs/guides/latest-model), [Sol](https://developers.openai.com/api/docs/models/gpt-6.2-sol), [Astra](https://developers.openai.com/api/docs/models/gpt-6.2-astra).
