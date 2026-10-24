import { expect, it } from "vitest";
import { type FlowInput, layoutFlow, NODE_WIDTH } from "./flow-layout";

const chain: FlowInput = {
  direction: "LR",
  nodes: [
    { id: "a", label: "A" },
    { id: "b", label: "B" },
    { id: "c", label: "C" },
  ],
  edges: [
    { source: "a", target: "b" },
    { source: "b", target: "c" },
  ],
};

it("lays a chain out left to right when no positions are given", () => {
  const at = layoutFlow(chain);
  const [a, b, c] = ["a", "b", "c"].map((id) => at.get(id));
  expect(a && b && c).toBeTruthy();
  expect(a!.x).toBeLessThan(b!.x);
  expect(b!.x).toBeLessThan(c!.x);
  expect(b!.x - a!.x).toBeGreaterThanOrEqual(NODE_WIDTH);
});

it("lays a chain out top to bottom when asked", () => {
  const at = layoutFlow({ ...chain, direction: "TB" });
  expect(at.get("a")!.y).toBeLessThan(at.get("b")!.y);
  expect(at.get("b")!.y).toBeLessThan(at.get("c")!.y);
});

it("keeps a position the agent gave", () => {
  const at = layoutFlow({
    ...chain,
    nodes: [{ id: "a", label: "A", position: { x: 500, y: 7 } }, ...chain.nodes.slice(1)],
  });
  expect(at.get("a")).toEqual({ x: 500, y: 7 });
});

it("places every node even when an edge points at nothing", () => {
  const at = layoutFlow({
    nodes: [{ id: "a", label: "A" }],
    edges: [{ source: "a", target: "ghost" }],
  });
  expect(at.has("a")).toBe(true);
});
