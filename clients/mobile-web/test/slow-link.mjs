import net from 'node:net';
import { Transform } from 'node:stream';

// Throttle actual TCP bytes so both HTTP assets and WebSocket frames traverse
// the same constrained link. Browser network emulation alone misses WS traffic.
export async function slowLink(upstreamPort, bytesPerSecond) {
  const sockets = new Set();
  const server = net.createServer(client => {
    const upstream = net.connect(upstreamPort, '127.0.0.1');
    sockets.add(client); sockets.add(upstream);
    let timer;
    const limiter = new Transform({
      transform(chunk, _encoding, done) {
        timer = setTimeout(() => done(null, chunk), chunk.length / bytesPerSecond * 1000);
      },
      destroy(error, done) { clearTimeout(timer); done(error); },
    });
    const close = () => { client.destroy(); upstream.destroy(); limiter.destroy(); sockets.delete(client); sockets.delete(upstream); };
    client.on('error', close); upstream.on('error', close);
    client.on('close', close); upstream.on('close', close);
    client.pipe(upstream); upstream.pipe(limiter).pipe(client);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return { port: server.address().port, close() { for (const socket of sockets) socket.destroy(); server.close(); } };
}
