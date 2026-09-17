import { Settings } from "lucide-react";
import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { ThemeToggle } from "@/components/ThemeToggle";
import { Button } from "@/components/ui/button";
import { closeRoom as closeRoomRequest, fetchRooms, type RoomSummary } from "@/lib/rooms-api";
import { cn } from "@/lib/utils";

/**
 * SessionListRoom is the room shape the tab strip renders.
 *
 * It is re-exported from the room API client rather than redeclared, so the
 * strip and the transport cannot drift. The name is kept for the modules that
 * already import it.
 */
export type SessionListRoom = RoomSummary;

const REFRESH_MS = 5000;

export function TabStrip() {
  const navigate = useNavigate();
  const location = useLocation();
  const [rooms, setRooms] = useState<SessionListRoom[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>("");

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const next = await fetchRooms();
        if (!cancelled) {
          setRooms(next);
          setError("");
        }
      } catch (err) {
        if (!cancelled) {
          setError((err as Error).message);
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
  const hitlActive = location.pathname === "/hitl" || location.pathname.startsWith("/hitl/items/");
  const turnsActive =
    location.pathname === "/turns" || location.pathname.startsWith("/turns/items/");
  const channelsActive = location.pathname.startsWith("/channels");
  const docsActive = location.pathname.startsWith("/docs");
  const settingsActive = location.pathname.startsWith("/settings");

  const closeRoom = async (roomID: string) => {
    try {
      await closeRoomRequest(roomID);
      const next = await fetchRooms();
      setRooms(next);
      setError("");
      if (activeRoomID === roomID) {
        navigate("/");
      }
    } catch (err) {
      setError((err as Error).message);
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
        <button
          type="button"
          onClick={() => navigate("/hitl")}
          aria-current={hitlActive ? "page" : undefined}
          className={cn(
            "shrink-0 border-l border-zinc-800 px-3 py-1 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-amber-400",
            hitlActive ? "text-amber-300" : "text-zinc-400 hover:text-zinc-100",
          )}
        >
          Approvals
        </button>
        <button
          type="button"
          onClick={() => navigate("/turns")}
          aria-current={turnsActive ? "page" : undefined}
          className={cn(
            "shrink-0 border-l border-zinc-800 px-3 py-1 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-amber-400",
            turnsActive ? "text-amber-300" : "text-zinc-400 hover:text-zinc-100",
          )}
        >
          Agent turns
        </button>
        <button
          type="button"
          onClick={() => navigate("/channels")}
          aria-current={channelsActive ? "page" : undefined}
          className={cn(
            "shrink-0 border-l border-zinc-800 px-3 py-1 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-amber-400",
            channelsActive ? "text-amber-300" : "text-zinc-400 hover:text-zinc-100",
          )}
        >
          Channels
        </button>
        <button
          type="button"
          onClick={() => navigate("/docs")}
          aria-current={docsActive ? "page" : undefined}
          className={cn(
            "shrink-0 border-l border-zinc-800 px-3 py-1 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-amber-400",
            docsActive ? "text-amber-300" : "text-zinc-400 hover:text-zinc-100",
          )}
        >
          Docs
        </button>
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
              const attached = room.connection_count ?? 0;
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
                  {attached > 1 ? (
                    <span
                      data-testid={`tab-strip-connections-${room.id}`}
                      title={`${attached} clients attached`}
                      className={cn(
                        "rounded-full px-1.5 text-[10px] leading-4",
                        active ? "bg-zinc-300 text-zinc-800" : "bg-zinc-800 text-zinc-400",
                      )}
                    >
                      {attached}
                    </span>
                  ) : null}
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
        {error ? (
          <div className="hidden text-xs text-red-300 sm:block" data-testid="tab-strip-error">
            {error}
          </div>
        ) : null}
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => void refreshRooms(setRooms, setError)}
        >
          Refresh
        </Button>
        <div className="flex shrink-0 items-center gap-1 border-l border-zinc-800 pl-2">
          <ThemeToggle />
          <button
            type="button"
            onClick={() => navigate("/settings")}
            aria-current={settingsActive ? "page" : undefined}
            aria-label="Settings"
            title="Settings"
            className={cn(
              "rounded p-1.5 outline-none focus-visible:ring-2 focus-visible:ring-amber-400",
              settingsActive ? "text-amber-300" : "text-zinc-400 hover:text-zinc-100",
            )}
          >
            <Settings className="size-4" aria-hidden="true" />
          </button>
        </div>
      </div>
    </div>
  );
}

async function refreshRooms(
  setRooms: (rooms: SessionListRoom[]) => void,
  setError: (error: string) => void,
) {
  try {
    setRooms(await fetchRooms());
    setError("");
  } catch (err) {
    setError((err as Error).message);
  }
}

function shortRoomID(roomID: string): string {
  return roomID.slice(0, 8);
}
