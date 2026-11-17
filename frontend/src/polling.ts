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
