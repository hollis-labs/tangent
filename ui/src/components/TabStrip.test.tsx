import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { TabStrip } from "./TabStrip";

describe("main navigation", () => {
  it("offers Inbox and Channels without expanding navigation for rooms", () => {
    render(
      <MemoryRouter initialEntries={["/"]}>
        <TabStrip />
      </MemoryRouter>,
    );
    expect(screen.getByRole("link", { name: "Inbox" })).toHaveAttribute("href", "/");
    expect(screen.getByRole("link", { name: "Channels" })).toHaveAttribute("href", "/channels");
    expect(screen.queryByRole("link", { name: "Approvals" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Agent turns" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Docs" })).not.toBeInTheDocument();
    expect(screen.queryByTestId("tab-strip")).not.toBeInTheDocument();
  });
  it("navigates back to the default Inbox from a room deep link", () => {
    render(
      <MemoryRouter initialEntries={["/r/room-a"]}>
        <TabStrip />
        <Routes>
          <Route path="/" element={<p>Default inbox</p>} />
          <Route path="*" element={<p>Selected interaction</p>} />
        </Routes>
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByRole("link", { name: "Inbox" }));
    expect(screen.getByText("Default inbox")).toBeInTheDocument();
  });
  it("places Chat between Inbox and Channels and leaves Inbox active state alone", () => {
    const { unmount } = render(
      <MemoryRouter initialEntries={["/"]}>
        <TabStrip />
      </MemoryRouter>,
    );
    const names = screen.getAllByRole("link").map((l) => l.textContent);
    expect(names.slice(1, 4)).toEqual(["Inbox", "Chat", "Channels"]);
    expect(screen.getByRole("link", { name: "Inbox" })).toHaveAttribute("aria-current", "page");
    expect(screen.getByRole("link", { name: "Chat" })).not.toHaveAttribute("aria-current");
    unmount();
    render(
      <MemoryRouter initialEntries={["/chat/pm"]}>
        <TabStrip />
      </MemoryRouter>,
    );
    expect(screen.getByRole("link", { name: "Chat" })).toHaveAttribute("aria-current", "page");
    expect(screen.getByRole("link", { name: "Inbox" })).not.toHaveAttribute("aria-current");
  });
});
