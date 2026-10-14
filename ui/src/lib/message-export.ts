// Copy and download for a single message, the same two actions the Docs
// toolbar offers for a whole document.

export async function copyText(text: string): Promise<void> {
  await navigator.clipboard.writeText(text);
}

export function safeFileName(label: string, fallback: string): string {
  return (
    label
      .replace(/[^a-zA-Z0-9 ._-]/g, "_")
      .trim()
      .slice(0, 120) || fallback
  );
}

export function downloadMarkdown(fileName: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: "text/markdown;charset=utf-8" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = `${fileName}.md`;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
