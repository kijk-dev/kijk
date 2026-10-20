import { loadScript } from '../diagram.js';

// A %%{init}%% line in a diagram cannot change these.
const SECURE = ['secure', 'securityLevel', 'startOnLoad', 'maxTextSize', 'suppressErrorRendering', 'maxEdges',
  'htmlLabels', 'dompurifyConfig', 'theme', 'themeVariables'];
let theme = '';
let n = 0;

export const load = () => loadScript('mermaid');

export async function draw(text, { vars, maxChars }) {
  await load();
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
