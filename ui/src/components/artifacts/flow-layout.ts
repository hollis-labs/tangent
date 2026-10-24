import dagre from "@dagrejs/dagre";

export type FlowKind = "trigger" | "action" | "condition" | "output" | "note";

/**
 * The agent-facing flow format. Positions are optional: nodes without one are laid out
 * automatically, so an agent describes the graph and not its geometry. Kept deliberately small and
 * free of any viewer detail so a workflow editor can reuse it.
 */
export interface FlowNodeInput {
  id: string;
  label: string;
  kind?: FlowKind;
  description?: string;
  position?: { x: number; y: number };
}
export interface FlowEdgeInput {
  id?: string;
  source: string;
  target: string;
  label?: string;
  animated?: boolean;
}
export interface FlowInput {
  direction?: "LR" | "TB";
  nodes: FlowNodeInput[];
  edges?: FlowEdgeInput[];
}

export const NODE_WIDTH = 220;
export const NODE_HEIGHT = 84;

/** Positions for every node: the given ones as they are, the rest from a layered layout. */
export function layoutFlow(flow: FlowInput): Map<string, { x: number; y: number }> {
  const direction = flow.direction === "TB" ? "TB" : "LR";
  const graph = new dagre.graphlib.Graph();
  graph.setDefaultEdgeLabel(() => ({}));
  graph.setGraph({ rankdir: direction, nodesep: 48, ranksep: 96 });
  for (const node of flow.nodes) graph.setNode(node.id, { width: NODE_WIDTH, height: NODE_HEIGHT });
  const ids = new Set(flow.nodes.map((node) => node.id));
  for (const edge of flow.edges ?? []) {
    if (ids.has(edge.source) && ids.has(edge.target)) graph.setEdge(edge.source, edge.target);
  }
  dagre.layout(graph);
  const out = new Map<string, { x: number; y: number }>();
  for (const node of flow.nodes) {
    if (node.position) {
      out.set(node.id, node.position);
      continue;
    }
    const placed = graph.node(node.id);
    out.set(node.id, {
      x: (placed?.x ?? 0) - NODE_WIDTH / 2,
      y: (placed?.y ?? 0) - NODE_HEIGHT / 2,
    });
  }
  return out;
}
