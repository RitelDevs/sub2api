import { describe, expect, it } from "vitest";

import {
  normalizeGroupOpenAIFast,
  supportsGroupOpenAIFast,
} from "../groupsOpenAIFast";
import en from "@/i18n/locales/en/admin/overview";
import zh from "@/i18n/locales/zh/admin/overview";

describe("groupsOpenAIFast", () => {
  it("supports OpenAI and composite groups", () => {
    expect(supportsGroupOpenAIFast("openai")).toBe(true);
    expect(supportsGroupOpenAIFast("composite")).toBe(true);
    expect(supportsGroupOpenAIFast("anthropic")).toBe(false);
  });

  it("clears stale enabled state on unsupported platforms", () => {
    expect(normalizeGroupOpenAIFast("openai", true)).toBe(true);
    expect(normalizeGroupOpenAIFast("composite", true)).toBe(true);
    expect(normalizeGroupOpenAIFast("anthropic", true)).toBe(false);
    expect(normalizeGroupOpenAIFast("openai", false)).toBe(false);
  });

  it("describes Fast and Ultrafast as independent settings in both locales", () => {
    for (const { groups } of [zh, en]) {
      expect(groups.openaiFast.hint).toContain("service_tier=priority");
      expect(groups.openaiFast.ultrafastHint).toContain("service_tier=ultrafast");
      expect(groups.openaiFast.freeHint).toContain("Standard");
      expect(groups.openaiFast.freeUltrafastHint).toContain("Standard");
    }
    expect(zh.groups.openaiFast.freeUltrafastHint).toContain("独立于免费 Fast");
    expect(en.groups.openaiFast.freeUltrafastHint).toContain("Independent of Free Fast");
  });

  it("limits the 3× promotion to GPT-6.2 and keeps GPT-6.1 Sol at 6×", () => {
    const zhHint = zh.groups.openaiFast.ultrafastHint;
    const enHint = en.groups.openaiFast.ultrafastHint;
    expect(zhHint).toMatch(/GPT-6\.2 Sol\/Astra[^。；]*2027-03-01[^。；]*3 倍计费，之后恢复 6 倍/);
    expect(enHint).toContain("GPT-6.2 Sol/Astra cost 3× Standard before March 1, 2027 00:00 Pacific, then 6×");
    expect(zhHint).toContain("GPT-6 Astra 和 GPT-6.1 Sol 为 6 倍");
    expect(enHint).toContain("GPT-6 Astra and GPT-6.1 Sol cost 6×");
  });
});
