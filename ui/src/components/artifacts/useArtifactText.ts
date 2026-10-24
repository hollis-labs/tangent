import { useEffect, useState } from "react";
import { fetchArtifactText } from "@/lib/artifacts-api";

/** Loads one revision's text, with the failure kept next to it. */
export function useArtifactText(itemID: string, revision: number) {
  const [state, setState] = useState<{ text?: string; error?: string }>({});
  useEffect(() => {
    let live = true;
    setState({});
    fetchArtifactText(itemID, revision)
      .then((text) => live && setState({ text }))
      .catch((reason: Error) => live && setState({ error: reason.message }));
    return () => {
      live = false;
    };
  }, [itemID, revision]);
  return state;
}
