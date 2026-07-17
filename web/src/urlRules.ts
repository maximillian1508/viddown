export type URLRule = {
  id: string;
  name: string;
  enabled: boolean;
  match: string;
  replace: string;
};

export type URLRulesConfig = {
  rules: URLRule[];
};

export type URLRewriteResult = {
  input: string;
  output: string;
  ruleId?: string;
  ruleName?: string;
  changed: boolean;
};

export function applyUrlRules(raw: string, cfg: URLRulesConfig): URLRewriteResult {
  const input = raw.trim();
  const base: URLRewriteResult = { input, output: input, changed: false };
  if (!input) return base;

  for (const rule of cfg.rules) {
    if (!rule.enabled || !rule.match) continue;
    let re: RegExp;
    try {
      re = new RegExp(rule.match);
    } catch {
      continue;
    }
    if (!re.test(input)) continue;
    const output = input.replace(re, rule.replace).trim();
    return {
      input,
      output,
      ruleId: rule.id,
      ruleName: rule.name,
      changed: output !== input,
    };
  }
  return base;
}

export function newURLRule(partial?: Partial<URLRule>): URLRule {
  return {
    id: crypto.randomUUID(),
    name: partial?.name ?? "New rule",
    enabled: partial?.enabled ?? true,
    match: partial?.match ?? "",
    replace: partial?.replace ?? "",
  };
}
