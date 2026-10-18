// SPDX-License-Identifier: AGPL-3.0-only
// A stub OpenRouter for the end-to-end tests: POST /api/v1/chat/completions
// answers by response_format.json_schema.name from fixtures/openrouter/<name>.json
// (an array of answers served in order, the last one repeating). It records
// nothing and never calls the real service.
//
//   node e2e/stub-openrouter.mjs [port]   (default 8181)

import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const dir = join(dirname(fileURLToPath(import.meta.url)), 'fixtures', 'openrouter');
const port = Number(process.argv[2] ?? process.env.STUB_PORT ?? 8181);
const served = new Map();

function answer(name) {
  let list;
  try {
    list = JSON.parse(readFileSync(join(dir, `${name}.json`), 'utf8'));
  } catch {
    return null;
  }
  const n = served.get(name) ?? 0;
  served.set(name, n + 1);
  const a = list[Math.min(n, list.length - 1)];
  return typeof a === 'string' ? a : JSON.stringify(a);
}

createServer((req, res) => {
  if (req.method === 'GET' && req.url === '/healthz') {
    res.end('ok');
    return;
  }
  if (req.method !== 'POST' || !req.url?.endsWith('/chat/completions')) {
    res.writeHead(404).end();
    return;
  }
  let body = '';
  req.on('data', (c) => (body += c));
  req.on('end', () => {
    let name = '';
    try {
      const j = JSON.parse(body);
      name = j.response_format?.json_schema?.name ?? '';
      if (j.provider?.data_collection !== 'deny' || !req.headers.authorization?.startsWith('Bearer ')) {
        res.writeHead(400, { 'Content-Type': 'application/json' }).end(JSON.stringify({ error: { message: 'stub: missing provider policy or key' } }));
        return;
      }
    } catch {
      res.writeHead(400).end();
      return;
    }
    const content = answer(name);
    if (content === null) {
      res.writeHead(404, { 'Content-Type': 'application/json' }).end(JSON.stringify({ error: { message: `stub: no fixture for ${name}` } }));
      return;
    }
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ choices: [{ finish_reason: 'stop', message: { role: 'assistant', content } }] }));
  });
}).listen(port, () => console.log(`stub OpenRouter on :${port}`));
