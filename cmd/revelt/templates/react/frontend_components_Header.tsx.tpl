// @mode ssr

interface HeaderProps {
  title: string;
}

export default function Header({ title }: HeaderProps) {
  return (
    <header style={{ background: "#333", color: "#fff", padding: "1rem" }}>
      <h1>{title}</h1>
      <small>Note: This header component is server-rendered only (no client JS loaded).</small>
    </header>
  );
}
