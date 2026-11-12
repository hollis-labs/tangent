import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import Room from "./Room";

const attach = vi.hoisted(() => vi.fn(() => () => {}));
const connectMock = vi.hoisted(() => vi.fn());

vi.mock("../lib/ws-client", () => ({ connect: connectMock }));
vi.mock("../hooks/useUiCommands", () => ({ useUiChannelAttachment: () => attach }));

describe("<Room> UI-command channel", () => {
  afterEach(() => vi.clearAllMocks());

  it("does not attach the channel, so no view frames go out, while the host has none", () => {
    const uiTransport = { connected: () => true, send: vi.fn(() => true), subscribe: vi.fn() };
    connectMock.mockImplementation(() => ({
      isConnected: () => true,
      close: vi.fn(),
      switchRoom: vi.fn(),
      uiTransport,
    }));
    render(
      <MemoryRouter initialEntries={["/r/room-a"]}>
        <Routes>
          <Route path="/r/:roomID" element={<Room />} />
        </Routes>
      </MemoryRouter>,
    );
    expect(connectMock).toHaveBeenCalled();
    expect(attach).not.toHaveBeenCalled();
    expect(uiTransport.send).not.toHaveBeenCalled();
  });
});
