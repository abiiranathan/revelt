// @mode client

import { useEffect, useState } from "react";

interface ClientChartProps {
  label: string;
}

export default function ClientChart({ label }: ClientChartProps) {
  const [data, setData] = useState<number[]>([]);

  useEffect(() => {
    setData([12, 19, 3, 5, 2, 3]);
  }, []);

  return (
    <div style={{ border: "2px dashed #ff007f", padding: "1rem", margin: "1rem 0" }}>
      <h3>{label}</h3>
      <p>Rendered client-side only (bypasses server-side Node rendering).</p>
      <div style={{ display: "flex", gap: "10px", alignItems: "flex-end", height: "100px", marginTop: "1rem" }}>
        {data.map((val, i) => (
          <div
            key={i}
            style={{
              width: "30px",
              height: (val * 4) + "px",
              backgroundColor: "#ff007f",
              textAlign: "center",
              color: "#fff",
              fontSize: "10px"
            }}
          >
            {val}
          </div>
        ))}
      </div>
    </div>
  );
}
