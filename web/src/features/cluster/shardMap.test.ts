import { buildMap } from "./shardMap";

test("counts shards with fewer serving copies than asked, on ready nodes only", () => {
  const nodes = [
    { name: "vdb-0", ordinal: 0, peer: 1, version: "v1", ready: true, downFor: 0 },
    { name: "vdb-1", ordinal: 1, peer: 2, version: "v1", ready: false, downFor: 3 },
  ];
  const m = buildMap(nodes, [
    { collection: "b", id: 0, replicas: [{ peer: 1, state: "Active" }, { peer: 2, state: "Active" }] },
    { collection: "a", id: 1, replicas: [{ peer: 1, state: "Active" }, { peer: 2, state: "Partial" }] },
    { collection: "a", id: 0, replicas: [{ peer: 1, state: "Active" }] },
  ], 1);
  expect(m.atRisk).toBe(0);
  expect(buildMap(nodes, [{ collection: "a", id: 0, replicas: [{ peer: 2, state: "Active" }] }], 1).atRisk).toBe(1);
  expect(m.groups.map((g) => g.collection)).toEqual(["a", "b"]);
  expect(m.groups[0]!.columns.map((c) => c.shard)).toEqual([0, 1]);
  expect(m.cell(2, m.groups[0]!.columns[1]!)).toBe("Partial");
  expect(m.cell(2, m.groups[0]!.columns[0]!)).toBeNull();
});
