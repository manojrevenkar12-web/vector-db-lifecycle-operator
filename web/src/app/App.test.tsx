import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import cluster from "../test/cluster.fixture.json";
import report from "../test/report.fixture.json";
import { App } from "./App";

type Handler = (url: string, init?: RequestInit) => unknown;

function serve(handler: Handler) {
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    const out = handler(String(url), init);
    if (out instanceof Error) throw out;
    if (out && typeof out === "object" && "status" in out && "body" in out) {
      const r = out as { status: number; body: unknown };
      return new Response(JSON.stringify(r.body), { status: r.status });
    }
    return new Response(JSON.stringify(out), { status: 200 });
  }));
}

const summary = { id: cluster.id, generation: cluster.generation, phase: cluster.status.phase, message: cluster.status.message, spec: cluster.spec };

afterEach(() => vi.unstubAllGlobals());

test("shows the fleet and the selected cluster's shard map", async () => {
  serve((u) => (u.endsWith("/v1/clusters") ? [summary] : u.includes("/v1/clusters/") ? cluster : report));
  render(<App />);
  expect(await screen.findByRole("heading", { name: cluster.id })).toBeInTheDocument();
  expect(screen.getByRole("table", { name: /where every shard's copies live/i })).toBeInTheDocument();
  expect(screen.getAllByRole("rowheader").length).toBe(cluster.observed.nodes.length);
});

test("a risky change shows that a person must approve it, and an agent's apply is refused", async () => {
  const user = userEvent.setup();
  let applied = 0;
  serve((u, init) => {
    if (u.endsWith("/changes/preview")) {
      return { status: 201, body: { id: "chg-1", cluster: cluster.id, generation: 1, from: cluster.spec, to: { ...cluster.spec, replicationFactor: 1 }, applied: false,
        effects: { restarts: 0, nodesAdded: 0, nodesRemoved: 0, replicasToMove: 0, temporaryCopies: 0, risks: ["one copy per shard"], needsPerson: true, needsPersonReason: ["fewer than 2 copies per shard"] } } };
    }
    if (u.endsWith("/apply")) {
      applied++;
      expect(new Headers(init?.headers).get("Idempotency-Key")).toMatch(/^ui-/);
      return { status: 403, body: { error: "this change needs a person's approval first" } };
    }
    return u.endsWith("/v1/clusters") ? [summary] : cluster;
  });
  render(<App />);
  const panel = await screen.findByRole("complementary", { name: /change this cluster/i });
  const rf = within(panel).getByLabelText(/copies per shard/i);
  await user.clear(rf);
  await user.type(rf, "1");
  await user.click(within(panel).getByRole("button", { name: /preview change/i }));
  expect(await within(panel).findByText(/needs a person: fewer than 2/i)).toBeInTheDocument();
  await user.click(within(panel).getByRole("button", { name: /^apply$/i }));
  expect(await within(panel).findByText(/refused \(403\)/i)).toBeInTheDocument();
  expect(applied).toBe(1);
});

test("says how to start the API when it is not reachable", async () => {
  serve(() => new TypeError("failed to fetch"));
  render(<App />);
  expect(await screen.findByRole("alert")).toHaveTextContent(/go run \.\/cmd\/api/);
});

test("shows the lab results from the generated report", async () => {
  const user = userEvent.setup();
  serve((u) => (u.includes("report.json") ? report : u.endsWith("/v1/clusters") ? [summary] : cluster));
  render(<App />);
  await user.click(screen.getByRole("button", { name: /lab results/i }));
  expect(await screen.findByRole("heading", { name: /same fleet, same failures/i })).toBeInTheDocument();
  expect(screen.getByRole("table", { name: /each safeguard switched off/i })).toBeInTheDocument();
  expect(screen.getAllByText("Pass").length).toBe(report.dev.gates.operator.length);
});
