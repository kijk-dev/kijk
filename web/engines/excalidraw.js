import { loadModule } from '../diagram.js';

export const load = () => loadModule('excalidraw');

// draw draws in kijk's light or dark colors, on no background, so the drawing sits on the page.
export async function draw(text, { theme, maxBytes }) {
  if (text.length > maxBytes) throw new Error('Diagram too large to draw');
  let scene = null;
  try {
    scene = JSON.parse(text);
  } catch {}
  if (scene?.type !== 'excalidraw' || !Array.isArray(scene.elements)) throw new Error('Excalidraw: this file is not a drawing');
  const elements = scene.elements.filter(x => !x.isDeleted);
  if (!elements.length) throw new Error('Excalidraw: the drawing is empty');
  const ex = await load();
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
