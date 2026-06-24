// @mode hydrate

import { useState } from "react";

interface CounterProps {
  title: string;
  initial?: number;
}

export default function Counter({ title, initial = 0 }: CounterProps) {
  const [count, setCount] = useState(initial);

  return (
    <div className="counter" style={{ border: "1px solid #ccc", padding: "1rem", margin: "1rem 0" }}>
      <h3>{title}</h3>
      <p className="counter__value">Count: {count}</p>
      <button onClick={() => setCount(prev => prev - 1)}>-</button>
      <button onClick={() => setCount(prev => prev + 1)}>+</button>
    </div>
  );
}
