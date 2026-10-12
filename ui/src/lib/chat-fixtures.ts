import type { TurnItemView } from "./turns-api";

export function turn(over: Partial<TurnItemView> & { item_id: string }): TurnItemView {
  return {
    contract_version: "1.2",
    agent_id: "pm",
    kind: "terminal",
    title: "t",
    content: "hello",
    state: "presented",
    queue_sequence: 1,
    revision: 2,
    created_at: "2026-10-12T10:00:00Z",
    updated_at: "2026-10-12T10:00:00Z",
    delivery_state: "queued",
    ...over,
  };
}
