import net from "node:net";
import http from "node:http";

const server = net.createServer((client) => {
  const upstream = net.createConnection({ host: "127.0.0.1", port: 443 });
  client.on("error", () => upstream.destroy());
  upstream.on("error", () => client.destroy());
  client.pipe(upstream).pipe(client);
});

server.listen(8444);

http.createServer((_request, response) => {
  response.writeHead(200, { "Content-Type": "application/json" });
  response.end('{"status":"ready"}');
}).listen(4176, "127.0.0.1");
