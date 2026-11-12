import { expect, it } from "vitest";
import { figmaURL } from "./figma-url";

it("accepts https figma.com links", () => {
  expect(figmaURL(" https://www.figma.com/file/abc/Name?node-id=1 ")).toBe(
    "https://www.figma.com/file/abc/Name?node-id=1",
  );
  expect(figmaURL("https://figma.com/design/abc")).toBe("https://figma.com/design/abc");
});

it("refuses everything else", () => {
  for (const raw of [
    "",
    "javascript:alert(1)",
    "data:text/html,<script>1</script>",
    "http://www.figma.com/file/abc",
    "https://evil.test/figma.com",
    "https://figma.com.evil.test/file",
    "https://notfigma.com/file",
    "https://user:pw@www.figma.com/file",
    "//www.figma.com/file",
    "/api/inbox",
  ]) {
    expect(figmaURL(raw), raw).toBeNull();
  }
});
