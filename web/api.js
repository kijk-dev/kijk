// A folder key names a folder on a machine. A local folder's key is its path, as before remote
// machines; a remote one's starts with a newline, which no absolute path does.
export const folderKey = (machine, root) => (machine ? `\n${machine}\n${root}` : root);

export function splitKey(key) {
  if (key?.[0] !== '\n') return { machine: '', root: key };
  const i = key.indexOf('\n', 1);
  return { machine: key.slice(1, i), root: key.slice(i + 1) };
}

// storeKey names a folder in localStorage, so local folders keep the state they had saved.
export function storeKey(key) {
  const { machine, root } = splitKey(key);
  return machine ? `m:${machine}:${root}` : root;
}

// base is where a machine's API lives: '' for Local, /m/<machine> for a remote machine.
export const base = machine => (machine ? `/m/${encodeURIComponent(machine)}` : '');

// route sends a call about a folder to the machine of its key, with the plain path as root.
function route(name, root) {
  if (root == null) return { url: `/api/${name}` };
  const k = splitKey(root);
  return { url: `${base(k.machine)}/api/${name}`, root: k.root };
}

// url is the address of a GET call, for what loads by URL, such as an image.
export function url(name, params) {
  const r0 = route(name, params?.root);
  return `${r0.url}?` + new URLSearchParams(r0.root == null ? params : { ...params, root: r0.root });
}

export async function get(name, params) {
  const r = await fetch(url(name, params));
  if (!r.ok) throw Object.assign(new Error((await r.text()).trim()), { status: r.status });
  return r.json();
}

export async function post(name, body) {
  const r0 = route(name, body?.root);
  const r = await fetch(r0.url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(r0.root == null ? body : { ...body, root: r0.root }),
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
