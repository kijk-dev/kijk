import { loadScript, loadModule } from '../diagram.js';

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

// PlantUML needs Graphviz and its themes as globals before its module loads.
export async function load() {
  await Promise.all([loadScript('graphviz'), loadScript('plantuml-themes')]);
  return loadModule('plantuml');
}

// Markdown hosts such as GitHub add @startuml and @enduml to a block that has none.
export async function draw(text, { theme, base, root, path, maxChars }) {
  if ([...text].length > maxChars) return { error: 'Diagram too large to draw', includes: [] };
  const pu = await load();
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
