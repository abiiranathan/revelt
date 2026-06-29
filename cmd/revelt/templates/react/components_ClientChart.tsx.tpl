// @mode client

import { useEffect, useState } from "react";

interface ClientChartProps {
  label: string;
}

export default function ClientChart({ label }: ClientChartProps) {
  const [data, setData] = useState<number[]>([]);
  const [hoveredIdx, setHoveredIdx] = useState<number | null>(null);

  useEffect(() => {
    setData([12, 19, 3, 5, 2, 8]);
  }, []);

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
            {label}
          </h3>
          <p style={{ fontSize: "0.875rem", color: "#64748b" }}>
            Bypasses server-side rendering. Executes client-side.
          </p>
        </div>
        <span style={{
          fontSize: "0.75rem",
          background: "rgba(236, 72, 153, 0.1)",
          color: "#ec4899",
          padding: "0.125rem 0.5rem",
          borderRadius: "9999px",
          fontWeight: 600,
          textTransform: "uppercase"
        }}>
          Client-Only
        </span>
      </div>

      <div style={{
        background: "#faf5ff",
        borderRadius: "12px",
        padding: "2rem 1.5rem 1rem",
        border: "1px solid #f3e8ff",
        height: "170px",
        display: "flex",
        alignItems: "flex-end",
        justifyContent: "space-around",
        gap: "10px",
        position: "relative"
      }}>
        {data.length === 0 ? (
          <div style={{ color: "#a855f7", fontSize: "0.875rem", width: "100%", textAlign: "center", alignSelf: "center" }}>
            Loading component...
          </div>
        ) : (
          data.map((val, i) => {
            const isHovered = hoveredIdx === i;
            return (
              <div
                key={i}
                onMouseEnter={() => setHoveredIdx(i)}
                onMouseLeave={() => setHoveredIdx(null)}
                style={{
                  flex: 1,
                  height: `${(val / 20) * 100}%`,
                  background: isHovered 
                    ? "linear-gradient(180deg, #d946ef 0%, #a855f7 100%)"
                    : "linear-gradient(180deg, #ec4899 0%, #8b5cf6 100%)",
                  borderRadius: "6px 6px 0 0",
                  transition: "all 0.2s ease-out",
                  cursor: "pointer",
                  display: "flex",
                  justifyContent: "center",
                  alignItems: "flex-start",
                  position: "relative",
                  boxShadow: isHovered ? "0 4px 12px rgba(168, 85, 247, 0.4)" : "none",
                  transform: isHovered ? "scaleY(1.05)" : "none"
                }}
              >
                <span style={{
                  fontSize: "10px",
                  color: "#ffffff",
                  fontWeight: 700,
                  marginTop: "6px",
                  opacity: isHovered ? 1 : 0.8,
                  transition: "opacity 0.2s"
                }}>
                  {val}
                </span>

                {isHovered && (
                  <div style={{
                    position: "absolute",
                    bottom: "105%",
                    background: "#1e293b",
                    color: "#ffffff",
                    padding: "4px 8px",
                    borderRadius: "4px",
                    fontSize: "10px",
                    fontWeight: 600,
                    whiteSpace: "nowrap",
                    boxShadow: "0 2px 4px rgba(0,0,0,0.1)",
                    zIndex: 10
                  }}>
                    Value: {val}
                  </div>
                )}
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
