import { useEffect, useState } from "react";

const API_URL = import.meta.env.VITE_API_URL || "http://localhost:8080";

interface Item {
  id: number;
  name: string;
  created_at: string;
}

function App() {
  const [items, setItems] = useState<Item[]>([]);
  const [name, setName] = useState("");
  const [source, setSource] = useState("");

  const fetchItems = async () => {
    const res = await fetch(`${API_URL}/api/items`);
    const data = await res.json();
    setItems(data.items || []);
    setSource(data.source || "");
  };

  useEffect(() => {
    fetchItems();
  }, []);

  const addItem = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    await fetch(`${API_URL}/api/items`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    setName("");
    fetchItems();
  };

  return (
    <div style={{ maxWidth: 600, margin: "0 auto", padding: 32 }}>
      <h1>POC B: Python API + React Frontend</h1>
      <p style={{ color: "#888" }}>
        API: {API_URL} | Data source: {source || "loading..."}
      </p>

      <form onSubmit={addItem} style={{ display: "flex", gap: 8, margin: "16px 0" }}>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Item name"
          style={{ flex: 1, padding: 8 }}
        />
        <button type="submit" style={{ padding: "8px 16px" }}>
          Add
        </button>
      </form>

      {items.length === 0 ? (
        <p style={{ color: "#aaa" }}>No items yet.</p>
      ) : (
        <ul>
          {items.map((item) => (
            <li key={item.id}>
              {item.name}{" "}
              <small style={{ color: "#aaa" }}>{item.created_at}</small>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export default App;
