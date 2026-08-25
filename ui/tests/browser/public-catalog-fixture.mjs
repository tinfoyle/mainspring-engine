import { createServer } from "node:http";
import process from "node:process";

const catalog = {
  version: 2,
  published_at: "2026-08-24T20:00:00Z",
  limits: [],
  packages: [{
    code: "work",
    version: 1,
    name: "Work",
    description: "Governed work with explicit ownership and lifecycle.",
    features: ["Your Turn review", "Assignments", "Lifecycle controls"]
  }],
  plans: [{
    code: "team",
    version: 1,
    name: "Team",
    description: "A governed operating workspace.",
    packages: { work: "enabled" }
  }],
  offers: [{
    code: "team-monthly-v1",
    plan_code: "team",
    plan_version: 1,
    currency: "USD",
    amount_minor: 4900,
    billing_interval: "month",
    effective_from: "2026-08-20T20:00:00Z"
  }]
};

const server = createServer((request, response) => {
  if (request.url === "/health/ready") {
    response.writeHead(204).end();
    return;
  }
  if (request.url === "/api/v1/catalog/public") {
    response.writeHead(200, { "Content-Type": "application/json" }).end(JSON.stringify(catalog));
    return;
  }
  response.writeHead(404, { "Content-Type": "application/problem+json" }).end(JSON.stringify({ status: 404 }));
});

server.listen(4175, "127.0.0.1");
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => server.close(() => process.exit(0)));
