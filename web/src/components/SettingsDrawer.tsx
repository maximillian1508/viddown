import { useEffect, useState } from "react";
import type { URLRulesConfig } from "../urlRules";
import { UrlRulesSettings } from "./UrlRulesSettings";
import "./settings.css";

type Section = "url-rules";

type Props = {
  open: boolean;
  onClose: () => void;
  urlRules: URLRulesConfig;
  onUrlRulesChange: (rules: URLRulesConfig) => void;
};

const sections: { id: Section; label: string }[] = [
  { id: "url-rules", label: "URL rules" },
];

export function SettingsDrawer({ open, onClose, urlRules, onUrlRulesChange }: Props) {
  const [section, setSection] = useState<Section>("url-rules");

  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => {
      document.body.style.overflow = prev;
      window.removeEventListener("keydown", onKey);
    };
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="settings-overlay" onClick={onClose}>
      <aside
        className="settings-drawer"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label="Settings"
      >
        <header className="settings-drawer-header">
          <h2 className="settings-drawer-title">Settings</h2>
          <button
            type="button"
            className="settings-close-btn"
            onClick={onClose}
            aria-label="Close settings"
          >
            ×
          </button>
        </header>

        <div className="settings-drawer-body">
          <nav className="settings-nav" aria-label="Settings sections">
            {sections.map((item) => (
              <button
                key={item.id}
                type="button"
                className={`settings-nav-item${section === item.id ? " is-active" : ""}`}
                onClick={() => setSection(item.id)}
              >
                {item.label}
                {item.id === "url-rules" && urlRules.rules.length > 0
                  ? ` (${urlRules.rules.length})`
                  : ""}
              </button>
            ))}
          </nav>

          <div className="settings-panel">
            {section === "url-rules" && (
              <UrlRulesSettings rules={urlRules} onChange={onUrlRulesChange} />
            )}
          </div>
        </div>
      </aside>
    </div>
  );
}
