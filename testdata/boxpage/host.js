// A page of another app that shows kijk's file boxes with diffs of its own, and no kijk server.
import { boxView, showBoxes } from '/kijk/boxes.js';
import { initLayout } from '/kijk/layout.js';
import { listenKeys } from '/kijk/keys.js';

const CART = `@@ -2,7 +2,7 @@
 // It knows the price of each item, and how many of each the shopper wants.
 export class Cart {
   constructor(prices) {
-    this.prices = prices;
+    this.prices = prices ?? {};
     this.items = new Map();
   }
   add(item) {
@@ -24,7 +24,7 @@ export class Cart {
     return this.items.get(item);
   }
   total(item) {
-    return this.items.get(item);
+    return [...this.items.values()].reduce((sum, n) => sum + n, 0);
   }
   empty(item) {
     return this.items.get(item);
@@ -39,6 +39,7 @@ export class Cart {
     return this.items.get(item);
   }
   merge(item) {
-    return this.items.get(item);
+    for (const [k, n] of item.items) this.items.set(k, n);
+    return this;
   }
 }`;

const CART_LATER = CART.replace('@@ -39,6 +39,7 @@', '@@ -39,6 +39,8 @@')
  .replace('+    for (const', '+    // An item in both carts takes the count of the other cart.\n+    for (const');

const PRICE = `@@ -1,4 +1,4 @@
-// price gives the price of an item in cents.
+// price gives the price of an item in cents, or 0 when it has none.
 export function price(prices, item) {
-  return prices[item] || 0;
+  return prices[item] ?? 0;
 }`;

// parseDiff turns the hunks of git's unified diff into those the boxes draw.
function parseDiff(text) {
  const hunks = [];
  let o = 0, n = 0;
  for (const line of text.split('\n')) {
    const m = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
    if (m) {
      o = +m[1];
      n = +m[2];
      hunks.push({ oldStart: o, newStart: n, lines: [] });
    } else if (line[0] === '+') hunks.at(-1).lines.push({ k: '+', n: n++, t: line.slice(1) });
    else if (line[0] === '-') hunks.at(-1).lines.push({ k: '-', o: o++, t: line.slice(1) });
    else hunks.at(-1).lines.push({ k: ' ', o: o++, n: n++, t: line.slice(1) });
  }
  return { hunks };
}

const diffs = { 'src/cart.js': CART, 'src/price.js': PRICE };
const count = (diff, k) => diff.split('\n').filter(l => l[0] === k).length;

const src = {
  files: () => Object.entries(diffs).map(([path, d]) => ({ path, orig: '', status: 'M', add: count(d, '+'), del: count(d, '-') })),
  basis: () => 'run 42',
  fetch: async f => parseDiff(diffs[f.path]),
  more: false,
};

listenKeys();
const viewer = document.getElementById('boxes');
initLayout(viewer, document.getElementById('layout'));
const v = boxView(viewer, 'run 42', {}, null, () => {}, src);
await showBoxes(v);

// The journey calls this as the app does when the agent changes a file.
globalThis.later = () => {
  diffs['src/cart.js'] = CART_LATER;
  return showBoxes(v, { paths: ['src/cart.js'] });
};
