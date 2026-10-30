// An HTTP proxy in front of the server under test that can make audio uploads fail at the connection level.
// Browser-side routing (page.route) can't do this reliably: a keepalive upload sent while the page unloads bypasses
// it. Every request, keepalive or not, goes through this proxy.
import { createServer, request, type IncomingHttpHeaders, type Server as HttpServer } from 'node:http';

const AUDIO_UPLOAD = /^\/api\/session\/[^/]+\/audio$/;

export class FailingProxy {
  readonly port: number;
  readonly target: number;
  readonly url: string;
  /** While true, every POST to /api/session/<take>/audio is cut off before it reaches the server. */
  failing = false;
  /** Uploads cut off so far. */
  failed = 0;
  private server: HttpServer;

  constructor(port: number, target: number) {
    this.port = port;
    this.target = target;
    this.url = `http://127.0.0.1:${port}/`;
    this.server = createServer((req, res) => {
      const path = new URL(req.url ?? '/', this.url).pathname;
      if (this.failing && req.method === 'POST' && AUDIO_UPLOAD.test(path)) {
        this.failed++;
        req.socket.destroy(); // a network error, as if the server were unreachable
        return;
      }
      const headers: IncomingHttpHeaders = { ...req.headers, host: `127.0.0.1:${target}` };
      // The server accepts POSTs only from its own origin: present the proxy's origin as the server's.
      if (headers.origin === `http://127.0.0.1:${port}` || headers.origin === `http://localhost:${port}`) {
        headers.origin = `http://127.0.0.1:${target}`;
      }
      const up = request({ host: '127.0.0.1', port: target, method: req.method, path: req.url, headers }, (ur) => {
        res.writeHead(ur.statusCode ?? 502, ur.headers);
        ur.pipe(res);
      });
      up.on('error', () => res.destroy());
      res.on('close', () => up.destroy()); // the browser cancels range requests while seeking
      req.pipe(up);
    });
  }

  start(): Promise<void> {
    return new Promise((resolve, reject) => {
      this.server.once('error', reject);
      this.server.listen(this.port, '127.0.0.1', () => resolve());
    });
  }

  stop(): Promise<void> {
    return new Promise((resolve) => {
      this.server.closeAllConnections();
      this.server.close(() => resolve());
    });
  }
}
