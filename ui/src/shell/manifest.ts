import type { FrontendManifest } from "@hollis-labs/app-shell";
import generated from "../generated/frontend.manifest.json";

/**
 * The shell reads one manifest. `substrate frontend` builds it (npm run manifest) from the modules the
 * binary links and from the app's own contribution file, `frontend.contributions` in
 * cmd/tangent/substrate.yaml, so nothing is merged here.
 */
export const manifest: FrontendManifest = generated as unknown as FrontendManifest;
