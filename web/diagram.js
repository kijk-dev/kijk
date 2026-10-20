// The frame that draws diagrams for the page. Its own CSP lets in the engines' inline styles, which
// the page's CSP blocks; the page gets each diagram back as SVG text and shows it as a picture.
const data = document.documentElement.dataset;
const engines = JSON.parse(data.engines); // from filetypes.go: each engine's label and the URLs of its files
const missing = new Set(data.missing.split(' ').filter(Boolean)); // engine files not in kijk's cache yet
const loaded = {};
let tries = 0;

// fetchFile gets one of the engines' files once, with add. After a failure the next diagram tries
// again; the server remembers the failure for a minute.
function fetchFile(name, add) {
  const engine = Object.values(engines).find(e => Object.hasOwn(e.files, name));
  loaded[name] ??= add(engine.files[name]).then(m => {
    missing.delete(name);
    return m;
  }, () => {
    delete loaded[name];
    tries++;
    throw new Error(`Could not download ${engine.label}`);
  });
  return loaded[name];
}

// loadScript adds an engine file as a classic script.
const loadScript = name => fetchFile(name, src => new Promise((resolve, reject) => {
  const s = document.createElement('script');
  s.src = src;
  s.onload = resolve;
  s.onerror = () => {
    s.remove();
    reject();
  };
  document.head.append(s);
}));

// loadModule imports an engine file. A failed import stays failed for its URL, so a retry asks for a new one.
const loadModule = name => fetchFile(name, src => import(src + (tries ? `&try=${tries}` : '')));

// A %%{init}%% line in a diagram cannot change these.
const SECURE = ['secure', 'securityLevel', 'startOnLoad', 'maxTextSize', 'suppressErrorRendering', 'maxEdges',
  'htmlLabels', 'dompurifyConfig', 'theme', 'themeVariables'];
let theme = '';
let n = 0;

async function mermaid(text, { vars, maxChars }) {
  await loadScript('mermaid');
  const m = globalThis.mermaid || globalThis.__esbuild_esm_mermaid_nm?.mermaid?.default;
  if (JSON.stringify(vars) !== theme) {
    theme = JSON.stringify(vars);
    m.initialize({
      startOnLoad: false, securityLevel: 'strict', htmlLabels: false, suppressErrorRendering: true,
      maxTextSize: maxChars, secure: SECURE, theme: 'base', themeVariables: vars, fontFamily: vars.fontFamily,
    });
  }
  const id = `k${++n}`;
  try {
    return { svg: (await m.render(id, text)).svg };
  } catch (err) {
    throw new Error(`Mermaid: ${String(err?.message || err).split('\n')[0].replace(/:$/, '')}`);
  } finally {
    document.getElementById(`d${id}`)?.remove(); // render leaves its scratch element behind
  }
}

const LIBRARY = "This diagram uses a PlantUML library kijk can't load yet";
const ICONS = "This diagram uses PlantUML emoji or icons kijk can't load yet";
const MAX_INCLUDES = 50;
let drawing = null; // the PlantUML diagram being drawn: {base, root, path, includes, calls}

// Libraries, emoji and icons would load as scripts from kijk's own server, which has none.
globalThis.PLANTUML_STDLIB_LOADER = (url, ok, err) => err(/emoji|openiconic/.test(url) ? ICONS : LIBRARY);

// Includes are read through the API of the folder's machine, relative to the including file.
globalThis.PLANTUML_FILE_LOADER = (path, from, ok, err) => {
  const at = drawing;
  const id = inFolder(path, from ?? at.path);
  if (id == null) return err(`${path} is outside the folder`);
  if (++at.calls > MAX_INCLUDES) return err('Too many includes');
  at.includes.add(id);
  fetch(`${at.base}/api/text?` + new URLSearchParams({ root: at.root, path: id }))
    .then(async r => {
      if (!r.ok) throw new Error((await r.text()).trim());
      return r.json();
    })
    .then(res => ok(id, res.text), e => err(e.message));
};

// inFolder resolves a path against the folder of the file `of`. null for an absolute path or one
// that climbs above the root: URL resolution would clamp it at the root instead.
function inFolder(path, of) {
  if (/^([a-z]:)?[\\/]/i.test(path)) return null;
  const parts = of.split('/').slice(0, -1);
  for (const s of path.split('/')) {
    if (s === '..') {
      if (!parts.length) return null;
      parts.pop();
    } else if (s && s !== '.') {
      parts.push(s);
    }
  }
  return parts.join('/');
}

// Markdown hosts such as GitHub add @startuml and @enduml to a block that has none. PlantUML needs
// Graphviz and its themes as globals before its module loads.
async function plantuml(text, { theme, base, root, path, maxChars }) {
  if ([...text].length > maxChars) return { error: 'Diagram too large to draw', includes: [] };
  await Promise.all([loadScript('graphviz'), loadScript('plantuml-themes')]);
  const pu = await loadModule('plantuml');
  const lines = text.split(/\r?\n/);
  const wrapped = !lines.some(l => /^\s*@start/.test(l));
  drawing = { base, root, path, includes: new Set(), calls: 0 };
  const includes = drawing.includes;
  const res = await new Promise(resolve => pu.renderToString(wrapped ? ['@startuml', ...lines, '@enduml'] : lines,
    svg => resolve({ svg, error: pictureError(svg, wrapped) }),
    err => resolve({ error: thrownError(err) }), { dark: theme === 'dark' }));
  return { ...(res.error ? { error: res.error } : { svg: res.svg }), includes: [...includes] };
}

function thrownError(err) {
  const msg = String(err?.message ?? err).replace(/^[\w.$]+(Exception|Error): /, '').replace(/\s*\(max \d+;.*\)$/, '');
  return [LIBRARY, ICONS].find(note => msg.includes(note)) ?? `PlantUML: ${msg}`;
}

// pictureError reads PlantUML's error picture, which comes back as a drawing rather than an error.
// Its first text is "PlantUML version …", or "An error has occurred!" for a crash.
function pictureError(svg, wrapped) {
  const texts = [...new DOMParser().parseFromString(svg, 'image/svg+xml').querySelectorAll('text')];
  const first = texts[0]?.textContent ?? '';
  if (first === 'An error has occurred!') return 'PlantUML could not draw this diagram';
  if (!first.startsWith('PlantUML version')) return null;
  const msg = (texts.find(t => t.getAttribute('fill') === '#FF0000') ?? texts.at(-1)).textContent.trim()
    .replace(/\s*\(Assumed diagram type: [^)]*\)$/, '');
  if (msg.startsWith('cannot include <')) return LIBRARY;
  // The first [From …] is where the error is: the diagram itself, or a file it includes.
  const from = texts.map(t => /^\[From (.*) \(line (\d+)\) \]$/.exec(t.textContent)).find(Boolean);
  if (!from) return `PlantUML: ${msg}`;
  const line = from[1] === 'textarea' ? `line ${from[2] - (wrapped ? 1 : 0)}` : `${from[1]}, line ${from[2]}`;
  return `PlantUML: ${msg} (${line})`;
}

// excalidraw draws in kijk's light or dark colors, on no background, so the drawing sits on the page.
async function excalidraw(text, { theme, maxBytes }) {
  if (text.length > maxBytes) throw new Error('Diagram too large to draw');
  let scene = null;
  try {
    scene = JSON.parse(text);
  } catch {}
  if (scene?.type !== 'excalidraw' || !Array.isArray(scene.elements)) throw new Error('Excalidraw: this file is not a drawing');
  const elements = scene.elements.filter(x => !x.isDeleted);
  if (!elements.length) throw new Error('Excalidraw: the drawing is empty');
  const ex = await loadModule('excalidraw');
  try {
    const svg = await ex.exportScene({
      type: 'svg', data: { elements, appState: scene.appState, files: scene.files },
      config: { theme, canvasBackgroundColor: false, padding: 16 },
    });
    return { svg: new XMLSerializer().serializeToString(svg) };
  } catch (err) {
    throw new Error(`Excalidraw: ${String(err?.message || err).split('\n')[0]}`);
  }
}

// sized gives the SVG its own width and height from its viewBox: as a picture, Mermaid's
// width="100%" has no size and the browser makes it 150 px high.
function sized(svg) {
  const tag = /<svg\b[^>]*>/.exec(svg)?.[0];
  const box = /\sviewBox="([^"]*)"/.exec(tag ?? '')?.[1].trim().split(/[\s,]+/).map(Number);
  if (!box || !(box[2] > 0 && box[3] > 0)) return { svg };
  const [width, height] = [Math.ceil(box[2]), Math.ceil(box[3])];
  const own = tag.replace(/\s(width|height|style)="[^"]*"/g, '').replace(/^<svg/, `<svg width="${width}" height="${height}"`);
  return { svg: svg.replace(tag, () => own), width, height };
}

const draw = { mermaid, plantuml, excalidraw };
let queue = Promise.resolve(); // one diagram at a time

addEventListener('message', e => {
  if (e.source !== parent || e.origin !== location.origin) return;
  const { id, engine, text, ctx } = e.data;
  const files = Object.hasOwn(engines, engine) ? Object.keys(engines[engine].files) : [];
  // Told on arrival, so a diagram that waits behind another one also says it is downloading.
  if (files.some(f => missing.has(f))) parent.postMessage({ id, downloading: engines[engine].label }, location.origin);
  queue = queue
    .then(() => (Object.hasOwn(draw, engine) ? draw[engine](text, ctx) : Promise.reject(new Error(`Unknown diagram kind ${engine}`))))
    .then(r => ({ id, ...r, ...(r.svg && sized(r.svg)) }), err => ({ id, error: err.message }))
    .then(msg => parent.postMessage(msg, location.origin));
});
parent.postMessage({ ready: true }, location.origin);
