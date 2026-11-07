const http = require('node:http');

function createErrorStyleProvider(requests, diagnostic) {
  let providerMode = 'auth', blockedRetry;
  const server = http.createServer((req, res) => {
    let raw = ''; req.on('data', chunk => { raw += chunk; }); req.on('end', () => {
      if (req.method === 'GET') return res.end(JSON.stringify({ data: [{ id: 'fixture' }] }));
      const body = JSON.parse(raw);
      // Pin the title role to a distinct fixture model instead of interpreting prompts.
      const role = body.model === 'fixture-title' ? 'title' : 'turn';
      requests.push({ path: req.url, stream: body.stream, mode: providerMode, role });
      if (!body.stream) { res.writeHead(200, { 'Content-Type': 'application/json' }); return res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'Recovery appearance fixture' }, finish_reason: 'stop' }] })); }
      if (role === 'title') return reply(res);
      if (providerMode === 'auth') { res.writeHead(401, { 'Content-Type': 'application/json' }); return res.end(JSON.stringify({ error: { message: diagnostic, type: 'authentication_error' } })); }
      if (providerMode === 'retry-blocked') { blockedRetry = res; return; }
      if (providerMode === 'retry') { providerMode = 'retry-blocked'; res.writeHead(503, { 'Content-Type': 'application/json', 'Retry-After': '2' }); return res.end(JSON.stringify({ error: { message: 'Synthetic service interruption before retry.' } })); }
      reply(res);
    });
  });
  function reply(res) {
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.write(`data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta: { role: 'assistant', content: 'Recovery completed through the local provider.' }, finish_reason: null }] })}\n\n`);
    res.end(`data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`);
  }
  return { server, reply, get mode() { return providerMode; }, set mode(value) { providerMode = value; }, get blockedRetry() { return blockedRetry; } };
}

module.exports = { createErrorStyleProvider };
