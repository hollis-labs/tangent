/**
 * Run `task` one at a time. A call that lands while it is running does not start a second
 * run; it marks the task dirty and shares the running promise, and exactly one follow-up
 * run happens afterwards. Used for list syncs, where a burst of revision events should cost
 * one fetch plus at most one conditional (304) re-check.
 */
export function createCoalescer(task: () => Promise<void>): () => Promise<void> {
  let running: Promise<void> | null = null;
  let dirty = false;
  return () => {
    if (running) {
      dirty = true;
      return running;
    }
    const drain = async () => {
      try {
        do {
          dirty = false;
          await task();
        } while (dirty);
      } finally {
        running = null;
      }
    };
    running = drain();
    return running;
  };
}
