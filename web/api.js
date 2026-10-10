export async function get(route, params) {
  const r = await fetch(`/api/${route}?` + new URLSearchParams(params));
  if (!r.ok) throw new Error((await r.text()).trim());
  return r.json();
}

export async function post(route, body) {
  const r = await fetch(`/api/${route}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const text = await r.text();
  if (!r.ok) throw new Error(text.trim());
  return JSON.parse(text);
}

export function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}
