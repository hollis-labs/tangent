import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useLocation, useParams } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@/routes/Inbox", () => ({
  default: () => {
    const { roomID, itemID } = useParams();
    return <p>inbox page {roomID ?? itemID ?? "list"}</p>;
  },
}));
vi.mock("@/routes/Chat", () => ({
  default: () => <p>chat page {useParams().agentID ?? "roster"}</p>,
}));
vi.mock("@/routes/ChannelPane", () => ({ default: () => <p>channels page</p> }));
vi.mock("@/routes/Settings", () => ({ default: () => <p>settings page</p> }));

import { TangentShell } from "./TangentShell";

function Where() {
  return <output data-testid="where">{useLocation().pathname}</output>;
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <TangentShell />
      <Where />
    </MemoryRouter>,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("<TangentShell>", () => {
  it("offers Tangent's destinations and the identity pages in the shell", async () => {
    renderAt("/");
    for (const name of [
      "Inbox",
      "Chat",
      "Channels",
      "Settings",
      "Account security",
      "Access keys",
      "Active sessions",
      "Sign out",
    ]) {
      expect(screen.getAllByRole("link", { name }).length).toBeGreaterThan(0);
    }
    expect(await screen.findByText("inbox page list")).toBeInTheDocument();
  });

  it("opens a room deep link on the Inbox destination with the room id", async () => {
    renderAt("/r/room-9");
    expect(await screen.findByText("inbox page room-9")).toBeInTheDocument();
    expect(screen.getByTestId("where")).toHaveTextContent("/r/room-9");
  });

  it("keeps one agent's chat on the Chat destination", async () => {
    renderAt("/chat/agent-3");
    expect(await screen.findByText("chat page agent-3")).toBeInTheDocument();
  });

  it("navigates between destinations through the shell's nav", async () => {
    renderAt("/");
    await screen.findByText("inbox page list");
    fireEvent.click(screen.getAllByRole("link", { name: "Channels" })[0]);
    expect(await screen.findByText("channels page")).toBeInTheDocument();
    expect(screen.getByTestId("where")).toHaveTextContent("/channels");
  });
});
