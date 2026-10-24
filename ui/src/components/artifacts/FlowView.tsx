import "@xyflow/react/dist/style.css";
import {
  Background,
  Controls,
  type Edge,
  Handle,
  MarkerType,
  MiniMap,
  type Node,
  type NodeProps,
  Position,
  ReactFlow,
} from "@xyflow/react";
import { useMemo } from "react";
import { type FlowInput, type FlowKind, layoutFlow, NODE_HEIGHT, NODE_WIDTH } from "./flow-layout";
import { useArtifactText } from "./useArtifactText";
import { useThemeMode } from "./useThemeMode";
import type { ViewerProps } from "./viewer-types";

const KIND_COLOR: Record<FlowKind, string> = {
  trigger: "#16a34a",
  action: "#2563eb",
  condition: "#d97706",
  output: "#9333ea",
  note: "#6b7280",
};

type StepData = { label: string; description?: string; kind: FlowKind; direction: "LR" | "TB" };

function StepNode({ data }: NodeProps<Node<StepData>>) {
  const horizontal = data.direction === "LR";
  return (
    <div
      className="rounded-lg border border-border bg-bg-elevated px-3 py-2 text-left shadow-sm"
      style={{
        width: NODE_WIDTH,
        minHeight: NODE_HEIGHT,
        borderLeft: `4px solid ${KIND_COLOR[data.kind]}`,
      }}
    >
      <Handle type="target" position={horizontal ? Position.Left : Position.Top} />
      <p className="text-[10px] uppercase tracking-wide text-fg-muted">{data.kind}</p>
      <p className="text-sm font-semibold">{data.label}</p>
      {data.description ? <p className="mt-1 text-xs text-fg-muted">{data.description}</p> : null}
      <Handle type="source" position={horizontal ? Position.Right : Position.Bottom} />
    </div>
  );
}

const nodeTypes = { step: StepNode };

/** Shows a node graph read-only: pan, zoom, minimap. Layout is automatic unless nodes carry positions. */
export default function FlowView({ itemID, revision }: ViewerProps) {
  const { text, error } = useArtifactText(itemID, revision);
  const mode = useThemeMode();
  const graph = useMemo(() => {
    if (text === undefined) return null;
    try {
      const flow = JSON.parse(text) as FlowInput;
      const direction = flow.direction === "TB" ? "TB" : "LR";
      const positions = layoutFlow(flow);
      const nodes: Node<StepData>[] = flow.nodes.map((node) => ({
        id: node.id,
        type: "step",
        position: positions.get(node.id) ?? { x: 0, y: 0 },
        data: {
          label: node.label,
          description: node.description,
          kind: node.kind ?? "action",
          direction,
        },
      }));
      const edges: Edge[] = (flow.edges ?? []).map((edge, index) => ({
        id: edge.id ?? `e${index}-${edge.source}-${edge.target}`,
        source: edge.source,
        target: edge.target,
        label: edge.label,
        animated: edge.animated,
        type: "smoothstep",
        markerEnd: { type: MarkerType.ArrowClosed },
      }));
      return { nodes, edges };
    } catch (reason) {
      return { problem: (reason as Error).message };
    }
  }, [text]);

  if (error)
    return (
      <p role="alert" className="p-4">
        {error}
      </p>
    );
  if (!graph)
    return (
      <p role="status" className="p-4">
        Loading…
      </p>
    );
  if ("problem" in graph)
    return (
      <p role="alert" className="p-4">
        This graph could not be read: {graph.problem}
      </p>
    );
  return (
    <div className="h-full" data-testid="artifact-flow">
      <ReactFlow
        key={revision}
        nodes={graph.nodes}
        edges={graph.edges}
        nodeTypes={nodeTypes}
        colorMode={mode}
        fitView
        nodesConnectable={false}
        proOptions={{ hideAttribution: true }}
      >
        <Background />
        <Controls showInteractive={false} />
        <MiniMap pannable zoomable />
      </ReactFlow>
    </div>
  );
}
