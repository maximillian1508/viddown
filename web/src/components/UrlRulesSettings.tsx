import { useState } from "react";
import {
  newURLRule,
  type URLRewriteResult,
  type URLRule,
  type URLRulesConfig,
} from "../urlRules";

async function readError(res: Response): Promise<string> {
  try {
    const data = await res.json();
    return data.error || res.statusText;
  } catch {
    return res.statusText;
  }
}

type Props = {
  rules: URLRulesConfig;
  onChange: (rules: URLRulesConfig) => void;
};

export function UrlRulesSettings({ rules, onChange }: Props) {
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [testIn, setTestIn] = useState("");
  const [testOut, setTestOut] = useState<URLRewriteResult | null>(null);
  const [testBusy, setTestBusy] = useState(false);

  function updateRule(id: string, patch: Partial<URLRule>) {
    onChange({
      rules: rules.rules.map((r) => (r.id === id ? { ...r, ...patch } : r)),
    });
  }

  function removeRule(id: string) {
    onChange({ rules: rules.rules.filter((r) => r.id !== id) });
  }

  async function save() {
    setSaving(true);
    setError(null);
    try {
      const res = await fetch("/api/url-rules", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(rules),
      });
      if (!res.ok) throw new Error(await readError(res));
      onChange(await res.json());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }

  async function testRules() {
    setTestBusy(true);
    setTestOut(null);
    setError(null);
    try {
      const res = await fetch("/api/url-rules/test", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url: testIn, rules: rules.rules }),
      });
      if (!res.ok) throw new Error(await readError(res));
      setTestOut(await res.json());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Test failed");
    } finally {
      setTestBusy(false);
    }
  }

  return (
    <div className="url-rules-settings">
      <p className="status">
        First matching enabled rule wins. Use regex capture groups like{" "}
        <code>$1</code> in replace.
      </p>

      {rules.rules.length === 0 && (
        <p className="status">No rules yet — add one for share links that need rewriting.</p>
      )}

      <ul className="url-rules-list">
        {rules.rules.map((rule) => (
          <li key={rule.id} className="url-rule-card">
            <div className="url-rule-head">
              <label className="url-rule-enable">
                <input
                  type="checkbox"
                  checked={rule.enabled}
                  onChange={(e) => updateRule(rule.id, { enabled: e.target.checked })}
                />
                <span>Enabled</span>
              </label>
              <button
                type="button"
                className="ghost url-rule-delete"
                onClick={() => removeRule(rule.id)}
              >
                Delete
              </button>
            </div>
            <label className="field">
              <span>Name</span>
              <input
                value={rule.name}
                onChange={(e) => updateRule(rule.id, { name: e.target.value })}
              />
            </label>
            <label className="field">
              <span>Match (regex)</span>
              <input
                value={rule.match}
                onChange={(e) => updateRule(rule.id, { match: e.target.value })}
                spellCheck={false}
                className="mono-input"
              />
            </label>
            <label className="field">
              <span>Replace</span>
              <input
                value={rule.replace}
                onChange={(e) => updateRule(rule.id, { replace: e.target.value })}
                spellCheck={false}
                className="mono-input"
              />
            </label>
          </li>
        ))}
      </ul>

      <div className="url-rules-actions">
        <button
          type="button"
          className="ghost"
          onClick={() =>
            onChange({
              rules: [
                ...rules.rules,
                newURLRule({
                  name: "MRDS share link",
                  match: String.raw`^https?://[^/]+/\?path=/archives/(\d+)/?$`,
                  replace: "https://mrds66.com/archives/$1/",
                }),
              ],
            })
          }
        >
          Add rule
        </button>
        <button type="button" className="primary" disabled={saving} onClick={() => void save()}>
          {saving ? "Saving…" : "Save rules"}
        </button>
      </div>

      <div className="url-rules-test">
        <label className="field">
          <span>Test URL</span>
          <div className="url-input-row">
            <input
              value={testIn}
              onChange={(e) => setTestIn(e.target.value)}
              placeholder="https://mrds36.com/?path=/archives/125828/"
              spellCheck={false}
            />
            <button
              type="button"
              className="ghost paste-btn"
              disabled={testBusy || !testIn.trim()}
              onClick={() => void testRules()}
            >
              {testBusy ? "…" : "Test"}
            </button>
          </div>
        </label>
        {testOut && (
          <p className="status">
            {testOut.changed ? (
              <>
                → <code>{testOut.output}</code>
                {testOut.ruleName ? ` · ${testOut.ruleName}` : ""}
              </>
            ) : (
              "No rule matched."
            )}
          </p>
        )}
      </div>

      {error && <p className="clipboard-error">{error}</p>}
    </div>
  );
}
