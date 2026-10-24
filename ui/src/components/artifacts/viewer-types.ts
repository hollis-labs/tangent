/** How an artifact's current revision is shown. One viewer per type. */
export interface ViewerProps {
  itemID: string;
  revision: number;
  title: string;
  /** Pixel width the preview is held to, or 0 for the full width. */
  width: number;
  /** Bumped by the Reload button to remount the frame. */
  reloadKey: number;
}
