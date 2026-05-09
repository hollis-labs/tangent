import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface SessionListRoom {
  id: string;
  title?: string;
  current_envelope_type?: string;
  created_at: string;
  updated_at: string;
}

const REFRESH_MS = 5000;

export function TabStrip() {
  const navigate = useNavigate();
  const location = useLocation();
  const [rooms, setRooms] = useState<SessionListRoom[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const next = await fetchRooms();
        if (!cancelled) {
          setRooms(next);
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    };
    void load();
    const timer = window.setInterval(() => {
      void load();
    }, REFRESH_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  const activeRoomID = location.pathname.startsWith("/r/") ? location.pathname.slice(3) : "";

  const closeRoom = async (roomID: string) => {
    await callTool("tangent.session_close", { roomID });
    const next = await fetchRooms();
    setRooms(next);
    if (activeRoomID === roomID) {
      navigate("/");
    }
  };

  return (
    <div className="border-b border-zinc-800 bg-zinc-950/95 backdrop-blur">
      <div className="mx-auto flex max-w-7xl items-center gap-3 px-4 py-3">
        <div className="shrink-0">
          <button
            type="button"
            onClick={() => navigate("/")}
            className="text-sm font-semibold tracking-wide text-zinc-100"
          >
            Tangent
          </button>
        </div>
        <div className="flex min-w-0 flex-1 gap-2 overflow-x-auto" data-testid="tab-strip">
          {rooms.length === 0 ? (
            <div className="rounded-full border border-dashed border-zinc-700 px-3 py-1 text-xs text-zinc-500">
              {loading ? "Loading rooms..." : "No active rooms"}
            </div>
          ) : (
            rooms.map((room) => {
              const active = room.id === activeRoomID;
              const label = room.title || shortRoomID(room.id);
              const suffix = room.current_envelope_type ? ` (${room.current_envelope_type})` : "";
              return (
                <div
                  key={room.id}
                  className={cn(
                    "flex items-center gap-2 rounded-full border px-3 py-1 text-xs transition-colors",
                    active
                      ? "border-zinc-100 bg-zinc-100 text-zinc-900"
                      : "border-zinc-700 bg-zinc-900 text-zinc-300",
                  )}
                >
                  <button
                    type="button"
                    onClick={() => navigate(`/r/${room.id}`)}
                    data-testid={`tab-strip-room-${room.id}`}
                    className="whitespace-nowrap"
                  >
                    {label}
                    {suffix}
                  </button>
                  <button
                    type="button"
                    onClick={() => void closeRoom(room.id)}
                    data-testid={`tab-strip-close-${room.id}`}
                    className={cn(
                      "rounded-full px-1 leading-none",
                      active
                        ? "text-zinc-700 hover:bg-zinc-300"
                        : "text-zinc-500 hover:bg-zinc-800",
                    )}
                    aria-label={`Close ${label}`}
                  >
                    ×
                  </button>
                </div>
              );
            })
          )}
        </div>
        <Button type="button" variant="ghost" size="sm" onClick={() => void refreshRooms(setRooms)}>
          Refresh
        </Button>
      </div>
    </div>
  );
}

async function refreshRooms(setRooms: (rooms: SessionListRoom[]) => void) {
  setRooms(await fetchRooms());
}

export async function fetchRooms(): Promise<SessionListRoom[]> {
  const result = await callTool("tangent.session_list", { active_only: true });
  const text = result?.result?.content?.[0]?.text;
  if (typeof text !== "string") {
    return [];
  }
  const parsed = JSON.parse(text) as { rooms?: SessionListRoom[] };
  return parsed.rooms ?? [];
}

async function callTool(name: string, args: Record<string, unknown>) {
  const response = await fetch("/mcp", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
    },
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method: "tools/call",
      params: {
        name,
        arguments: args,
      },
    }),
  });
  if (!response.ok) {
    throw new Error(`tool ${name} HTTP ${response.status}`);
  }
  return response.json();
}

function shortRoomID(roomID: string): string {
  return roomID.slice(0, 8);
}
