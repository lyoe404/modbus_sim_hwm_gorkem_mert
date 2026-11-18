// Share one in-flight cycle between timer ticks and manual refreshes.
export function createPolling(task: (signal: AbortSignal) => Promise<void>, interval: number) {
  let stopped = false;
  let pending: Promise<void> | undefined;
  let controller: AbortController | undefined;
  const run = (): Promise<void> => {
    if (stopped) return Promise.resolve();
    if (pending) return pending;
    controller = new AbortController();
    const signal = controller.signal;
    pending = Promise.resolve().then(() => task(signal)).finally(() => { pending = undefined; });
    return pending;
  };
  const timer = window.setInterval(() => { void run().catch(() => {}); }, interval);
  void run().catch(() => {});
  return {
    run,
    stop() {
      stopped = true;
      window.clearInterval(timer);
      controller?.abort();
    },
  };
}

// A command invalidates earlier polls. Polls that start during a command
// cannot know whether their snapshot preceded the physical change.
export function createSnapshotGuard() {
  let revision = 0;
  let pending = 0;
  return {
    get busy() { return pending !== 0; },
    capture() { return pending === 0 ? revision : undefined; },
    accepts(snapshot: number | undefined) { return pending === 0 && snapshot === revision; },
    begin() {
      const operation = ++revision;
      pending++;
      return {
        current: () => operation === revision,
        finish: () => { pending--; },
      };
    },
  };
}
