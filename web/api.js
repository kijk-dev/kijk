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
  if (!r.ok) throw Object.assign(new Error(text.trim()), { status: r.status });
  return JSON.parse(text);
}

export function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}

// svg is an icon in a span, or in tag; markup is always one of the fixed strings in the code.
export function svg(markup, cls, tag = 'span') {
  const s = el(tag, cls);
  s.innerHTML = markup;
  return s;
}

// icon is the svg element of markup, one of the fixed strings in the code.
export const icon = markup => Object.assign(document.createElement('template'), { innerHTML: markup }).content.firstChild;

// h describes an element for patch. props is a class name, or {key, class, attrs, data, html,
// autofocus, and any property, such as onclick, value or disabled}.
export const h = (tag, props, ...children) => ({ tag, props: typeof props === 'string' ? { class: props } : props || {}, children });

const flat = (list, out = []) => {
  for (const c of list) {
    if (Array.isArray(c)) flat(c, out);
    else if (c != null && c !== false && c !== '') out.push(typeof c === 'number' ? String(c) : c);
  }
  return out;
};

// patch makes parent's children what the descriptions say: text, descriptions, arrays, null or
// false, and elements, put as they are. It keeps each element whose tag and key match, in order for
// those with no key, so what has the focus, a caret or a scroll place keeps it.
export function patch(parent, ...children) {
  const want = flat(children);
  const had = globalThis.kijkCheck && parent.contains(document.activeElement) && document.activeElement;
  const old = [...parent.childNodes], keyed = new Map(), loose = new Map(), used = new Set();
  for (const c of old) {
    const k = c.drawn?.props.key;
    if (k != null) keyed.set(`${c.nodeName}\n${k}`, c);
    else if (c.drawn || c.nodeType === 3) (loose.get(c.nodeName) || loose.set(c.nodeName, { at: 0, list: [] }).get(c.nodeName)).list.push(c);
  }
  const next = name => {
    const l = loose.get(name);
    while (l && l.at < l.list.length) if (!used.has(l.list[l.at++])) return l.list[l.at - 1];
  };
  const nodes = want.map(w => {
    let node = w;
    if (typeof w === 'string') {
      node = next('#text') || document.createTextNode(w);
      if (node.data !== w) node.data = w;
    } else if (!(w instanceof Node)) {
      const name = w.tag.toUpperCase();
      node = w.props.key != null ? keyed.get(`${name}\n${w.props.key}`) : next(name);
      if (!node || used.has(node)) node = document.createElement(w.tag);
    }
    used.add(node);
    return node;
  });
  for (const c of old) if (!used.has(c)) c.remove();
  let at = parent.firstChild;
  for (const node of nodes) {
    if (node === at) at = at.nextSibling;
    else if (parent.moveBefore && node.isConnected && parent.isConnected) parent.moveBefore(node, at);
    else parent.insertBefore(node, at);
  }
  want.forEach((w, i) => typeof w === 'object' && !(w instanceof Node) && update(nodes[i], w));
  // For the tests: the focus went because an element was made again instead of kept.
  if (had && !had.isConnected) {
    const was = old.find(c => c.contains(had)), before = new Set(old);
    if (was && nodes.some(n => !before.has(n) && n.nodeName === was.nodeName && n.className === was.className)) {
      throw new Error(`patch made the focused ${was.nodeName.toLowerCase()}.${was.className} again`);
    }
  }
}

const LIVE = new Set(['value', 'checked']); // the user changes these, so they compare with the page

function update(node, d) {
  const was = node.drawn?.props || {}, props = d.props;
  for (const [k, v] of Object.entries(props)) {
    if (k === 'attrs' || k === 'data') {
      const o = was[k] || {};
      for (const a in v) if (o[a] !== v[a]) k === 'data' ? (node.dataset[a] = v[a]) : node.setAttribute(a, v[a]);
      for (const a in o) if (!(a in v)) k === 'data' ? delete node.dataset[a] : node.removeAttribute(a);
    } else if (k === 'class') {
      if (node.className !== v) node.className = v;
    } else if (k === 'html') {
      if (v !== was.html) node.innerHTML = v; // one of the fixed strings in the code
    } else if (k === 'autofocus') {
      if (v && !node.drawn) queueMicrotask(() => node.isConnected && node.focus({ preventScroll: true }));
    } else if (k !== 'key' && (LIVE.has(k) ? node[k] !== v : was[k] !== v || typeof v === 'function')) {
      node[k] = v;
    }
  }
  for (const [k, v] of Object.entries(was)) {
    if (k in props) continue;
    if (k === 'attrs' || k === 'data') for (const a in v) k === 'data' ? delete node.dataset[a] : node.removeAttribute(a);
    else if (typeof v === 'function' || typeof v === 'boolean') node[k] = typeof v === 'function' ? null : false;
    else if (k !== 'html') node.removeAttribute(k.toLowerCase());
  }
  node.drawn = d;
  if (props.html == null) patch(node, d.children);
}
