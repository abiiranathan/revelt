// @mode hydrate

import { useState } from "react";

interface CounterProps {
  title: string;
  initial?: number;
}

export default function Counter({ title, initial = 0 }: CounterProps) {
  const [count, setCount] = useState(initial);

  return (
    <div style={{
      background: "#ffffff",
      border: "1px solid #e2e8f0",
      borderRadius: "16px",
      padding: "2rem",
      boxShadow: "0 4px 6px -1px rgba(0, 0, 0, 0.05), 0 2px 4px -1px rgba(0, 0, 0, 0.03)",
      display: "flex",
      flexDirection: "column",
      gap: "1.25rem"
    }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start" }}>
        <div>
          <h3 style={{ fontSize: "1.25rem", fontWeight: 700, color: "#1e293b", letterSpacing: "-0.01em", marginBottom: "0.25rem" }}>
            {title}
          </h3>
          <p style={{ fontSize: "0.875rem", color: "#64748b" }}>
            Interactive state preserved and hydrated dynamically.
          </p>
        </div>
        <span style={{
          fontSize: "0.75rem",
          background: "#f1f5f9",
          color: "#64748b",
          padding: "0.125rem 0.5rem",
          borderRadius: "9999px",
          fontWeight: 600,
          textTransform: "uppercase"
        }}>
          Hydrated
        </span>
      </div>

      <div style={{
        background: "#f8fafc",
        borderRadius: "12px",
        padding: "1.5rem",
        textAlign: "center",
        border: "1px solid #f1f5f9"
      }}>
        <div style={{ fontSize: "0.75rem", textTransform: "uppercase", letterSpacing: "0.05em", color: "#94a3b8", fontWeight: 600, marginBottom: "0.25rem" }}>
          Current Value
        </div>
        <div style={{ fontSize: "2.75rem", fontWeight: 800, color: "#0f172a", fontFamily: "monospace" }}>
          {count}
        </div>
      </div>

      <div style={{ display: "flex", gap: "0.75rem" }}>
        <button
          onClick={() => setCount(prev => prev - 1)}
          style={{
            flex: 1,
            padding: "0.75rem",
            background: "#ffffff",
            border: "1px solid #cbd5e1",
            borderRadius: "8px",
            fontWeight: 600,
            color: "#334155",
            cursor: "pointer",
            fontSize: "1.125rem",
            display: "inline-flex",
            justifyContent: "center",
            alignItems: "center",
            transition: "all 0.15s ease"
          }}
          onMouseOver={(e) => {
            e.currentTarget.style.borderColor = "#6366f1";
            e.currentTarget.style.color = "#6366f1";
          }}
          onMouseOut={(e) => {
            e.currentTarget.style.borderColor = "#cbd5e1";
            e.currentTarget.style.color = "#334155";
          }}
        >
          -
        </button>
        <button
          onClick={() => setCount(prev => prev + 1)}
          style={{
            flex: 1,
            padding: "0.75rem",
            background: "#6366f1",
            border: "1px solid transparent",
            borderRadius: "8px",
            fontWeight: 600,
            color: "#ffffff",
            cursor: "pointer",
            fontSize: "1.125rem",
            display: "inline-flex",
            justifyContent: "center",
            alignItems: "center",
            transition: "all 0.15s ease",
            boxShadow: "0 2px 4px rgba(99, 102, 241, 0.2)"
          }}
          onMouseOver={(e) => {
            e.currentTarget.style.background = "#4f46e5";
          }}
          onMouseOut={(e) => {
            e.currentTarget.style.background = "#6366f1";
          }}
        >
          +
        </button>
      </div>
    </div>
  );
}
