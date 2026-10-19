// The frame that draws diagrams for the page. Its own CSP lets Mermaid's styles in, which the
// page's CSP blocks; the page gets each diagram back as SVG text and shows it as a picture.
const labels = { mermaid: 'Mermaid' };
const loading = {};

// load adds the engine's script once. After a failure the next diagram tries again; the server
// remembers the failure for a minute.
function load(kind) {
  loading[kind] ??= new Promise((resolve, reject) => {
    const s = document.createElement('script');
    s.src = document.documentElement.dataset[kind];
    s.onload = resolve;
    s.onerror = () => {
      delete loading[kind];
      s.remove();
      reject(new Error(`Could not download ${labels[kind]}`));
    };
    document.head.append(s);
  });
  return loading[kind];
}

// A %%{init}%% line in a diagram cannot change these.
const SECURE = ['secure', 'securityLevel', 'startOnLoad', 'maxTextSize', 'suppressErrorRendering', 'maxEdges',
  'htmlLabels', 'dompurifyConfig', 'theme', 'themeVariables'];
let theme = '';
let n = 0;

async function mermaid(text, vars) {
  await load('mermaid');
  const m = globalThis.mermaid || globalThis.__esbuild_esm_mermaid_nm?.mermaid?.default;
  if (JSON.stringify(vars) !== theme) {
    theme = JSON.stringify(vars);
    m.initialize({
      startOnLoad: false, securityLevel: 'strict', htmlLabels: false, suppressErrorRendering: true,
      maxTextSize: 50000, secure: SECURE, theme: 'base', themeVariables: vars, fontFamily: vars.fontFamily,
    });
  }
  const id = `k${++n}`;
  try {
    return (await m.render(id, text)).svg;
  } catch (err) {
    throw new Error(`Mermaid: ${String(err?.message || err).split('\n')[0].replace(/:$/, '')}`);
  } finally {
    document.getElementById(`d${id}`)?.remove(); // render leaves its scratch element behind
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

const draw = { mermaid };
let queue = Promise.resolve(); // one diagram at a time

addEventListener('message', e => {
  if (e.source !== parent || e.origin !== location.origin) return;
  const { id, kind, text, vars } = e.data;
  queue = queue
    .then(() => (draw[kind] ? draw[kind](text, vars) : Promise.reject(new Error(`Unknown diagram kind ${kind}`))))
    .then(svg => ({ id, ...sized(svg) }), err => ({ id, error: err.message }))
    .then(msg => parent.postMessage(msg, location.origin));
});
parent.postMessage({ ready: true }, location.origin);
