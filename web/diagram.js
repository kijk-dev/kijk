// The frame that draws diagrams for the page. Its own CSP lets in the engines' inline styles, which
// the page's CSP blocks; the page gets each diagram back as SVG text and shows it as a picture.

// Each engine is engines/<type name>.js. It exports load(), which gets its files with loadScript
// and loadModule, and draw(text, ctx), which calls load once it needs the engine.
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
export const loadScript = name => fetchFile(name, src => new Promise((resolve, reject) => {
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
export const loadModule = name => fetchFile(name, src => import(src + (tries ? `&try=${tries}` : '')));

// sized gives the SVG its own width and height from its viewBox: as a picture, an SVG with
// width="100%" has no size and the browser makes it 150 px high.
function sized(svg) {
  const tag = /<svg\b[^>]*>/.exec(svg)?.[0];
  const box = /\sviewBox="([^"]*)"/.exec(tag ?? '')?.[1].trim().split(/[\s,]+/).map(Number);
  if (!box || !(box[2] > 0 && box[3] > 0)) return { svg };
  const [width, height] = [Math.ceil(box[2]), Math.ceil(box[3])];
  const own = tag.replace(/\s(width|height|style)="[^"]*"/g, '').replace(/^<svg/, `<svg width="${width}" height="${height}"`);
  return { svg: svg.replace(tag, () => own), width, height };
}

let queue = Promise.resolve(); // one diagram at a time

addEventListener('message', e => {
  if (e.source !== parent || e.origin !== location.origin) return;
  const { id, engine, text, ctx } = e.data;
  const known = Object.hasOwn(engines, engine);
  // Told on arrival, so a diagram that waits behind another one also says it is downloading.
  if (known && Object.keys(engines[engine].files).some(f => missing.has(f))) {
    parent.postMessage({ id, downloading: engines[engine].label }, location.origin);
  }
  const draw = () => import(`./engines/${engine}.js`).then(m => m.draw(text, ctx));
  queue = queue
    .then(() => (known ? draw() : Promise.reject(new Error(`Unknown file type ${engine}`))))
    .then(r => ({ id, ...r, ...(r.svg && sized(r.svg)) }), err => ({ id, error: err.message }))
    .then(msg => parent.postMessage(msg, location.origin));
});
parent.postMessage({ ready: true }, location.origin);
