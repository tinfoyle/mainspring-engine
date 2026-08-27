import { createServer } from "node:http";
import process from "node:process";

const catalog = {
  version: 3,
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
    version: 2,
    name: "Infinite Ocean Team",
    description: "The complete Infinite Ocean operating system for one team.",
    packages: { work: "enabled" }
  }],
  offers: [{
    code: "team-monthly-v2",
    plan_code: "team",
    plan_version: 2,
    currency: "USD",
    amount_minor: 5000,
    billing_interval: "month",
    effective_from: "2026-08-20T20:00:00Z"
  }],
  ai_token_renewal_grant: {
    code: "team_renewal_v1",
    version: 1,
    quantity: 10000,
    disclosure: "Included per successful team subscription renewal."
  },
  ai_token_bundles: [{
    code: "tokens_10k_v1",
    version: 1,
    quantity: 10000,
    currency: "USD",
    amount_minor: 1000,
    effective_from: "2026-08-20T20:00:00Z",
    disclosure: "Purchased AI Tokens remain with the active team."
  }],
  commissioning_offer: {
    code: "commissioning_v1",
    version: 1,
    currency: "USD",
    amount_minor: 25000,
    effective_from: "2026-08-20T20:00:00Z",
    disclosure: "Collaborative setup and configuration for one team."
  },
  ai_complexity_rates: ["simple", "efficient", "balanced", "thorough", "advanced"].map((complexity, index) => ({
    complexity,
    code: `${complexity}_v1`,
    version: 1,
    input_per_thousand: 1 + index,
    cached_input_per_thousand: 1 + index,
    output_per_thousand: 4 + index * 4,
    tool_invocation: 10 + index * 10,
    minimum_charge: 5 + index * 5,
    maximum_reservation: 1000 + index * 1000,
    estimated_minimum: 10 + index * 10,
    estimated_maximum: 100 + index * 100
  }))
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
