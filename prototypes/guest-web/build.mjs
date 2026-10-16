// PROTOTYPE — 把 shared.js 内联进每个候选，产出可单独打开的 dist/*.html
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
const shared = readFileSync(new URL('./shared.js', import.meta.url), 'utf8');
mkdirSync(new URL('./dist/', import.meta.url), { recursive: true });
for (const n of ['a', 'b', 'c']) {
  const html = readFileSync(new URL(`./${n}.html`, import.meta.url), 'utf8').replace('/*SHARED*/', () => shared);
  writeFileSync(new URL(`./dist/${n}.html`, import.meta.url), html);
  for (const [, js] of html.matchAll(/<script>([\s\S]*?)<\/script>/g)) new Function(js); // 语法检查
}
console.log('ok');
