// @mode ssr

interface HeaderProps {
  title: string;
}

export default function Header({ title }: HeaderProps) {
  return (
    <header style={{
      background: "linear-gradient(135deg, #0f172a 0%, #1e293b 100%)",
      color: "#f8fafc",
      padding: "3rem 1.5rem",
      borderBottom: "1px solid #334155",
      textAlign: "center",
      boxShadow: "0 4px 20px rgba(0,0,0,0.08)"
    }}>
      <div style={{ maxWidth: "960px", margin: "0 auto", padding: "0 1.5rem" }}>
        <h1 style={{
          fontSize: "2.5rem",
          fontWeight: 800,
          letterSpacing: "-0.025em",
          marginBottom: "0.75rem",
          background: "linear-gradient(to right, #818cf8, #c084fc)",
          WebkitBackgroundClip: "text",
          WebkitTextFillColor: "transparent"
        }}>
          {title}
        </h1>
        <div style={{
          display: "inline-flex",
          alignItems: "center",
          gap: "0.5rem",
          background: "rgba(99, 102, 241, 0.1)",
          border: "1px solid rgba(99, 102, 241, 0.2)",
          borderRadius: "9999px",
          padding: "0.25rem 0.75rem",
          fontSize: "0.875rem",
          color: "#a5b4fc",
          fontWeight: 500
        }}>
          <span style={{
            width: "6px",
            height: "6px",
            background: "#4ade80",
            borderRadius: "50%",
            display: "inline-block"
          }}></span>
          revelt server-rendered component (no client overhead)
        </div>
      </div>
    </header>
  );
}
