export type RunOptions = { signal?: AbortSignal; stdin?: string; timeout?: number }
export type Run = (argv: string[], options?: RunOptions) => Promise<string>

// One pool per plugin instance, shared by observers and explicit tools. Callers
// own command/config validation; neither argv nor cwd is exposed as a tool arg.
export function createRunner(cwd: () => Promise<string> | string) {
  const cancellers = new Set<() => void>()
  let disposed = false

  function at(directory: () => Promise<string> | string): Run {
    return async (argv, options = {}) => {
      if (disposed) throw new Error("process runner disposed")
      if (options.signal?.aborted) throw new Error("process aborted")
      if (cancellers.size >= 3) throw new Error("process concurrency limit reached")
      const timeout = options.timeout ?? 5000
      if (!Number.isFinite(timeout) || timeout <= 0) throw new Error("invalid process timeout")
      let proc: ReturnType<typeof Bun.spawn> | undefined
      let deadline: ReturnType<typeof setTimeout> | undefined
      let stopped = false
      let cancel!: () => void
      const interrupted = new Promise<never>((_resolve, reject) => {
        cancel = () => {
          stopped = true
          try { proc?.kill() } catch { /* already exited */ }
          reject(new Error("process timed out, aborted or disposed"))
        }
      })
      cancellers.add(cancel)
      options.signal?.addEventListener("abort", cancel, { once: true })
      deadline = setTimeout(cancel, timeout)
      deadline.unref?.()

      async function drain(stream: ReadableStream<Uint8Array>, limit: number) {
        const reader = stream.getReader()
        const decoder = new TextDecoder()
        let text = "", size = 0
        try {
          while (true) {
            const { value, done } = await reader.read()
            if (done) return text + decoder.decode()
            size += value.byteLength
            if (size > limit) throw new Error("process output exceeded limit")
            text += decoder.decode(value, { stream: true })
          }
        } finally { reader.releaseLock() }
      }

      try {
        return await Promise.race([interrupted, (async () => {
          const cwd = await directory()
          if (stopped || disposed || options.signal?.aborted) throw new Error("process aborted or disposed")
          proc = Bun.spawn(argv, {
            cwd, stdout: "pipe", stderr: "pipe",
            stdin: options.stdin === undefined ? "ignore" : new TextEncoder().encode(options.stdin),
          })
          const [out, stderr, code] = await Promise.all([
            drain(proc.stdout as ReadableStream<Uint8Array>, 4 * 1024 * 1024),
            drain(proc.stderr as ReadableStream<Uint8Array>, 64 * 1024), proc.exited,
          ])
          if (code !== 0) throw new Error(`process exited ${code}: ${stderr.trim()}`)
          return out
        })()])
      } finally {
        stopped = true
        if (deadline) clearTimeout(deadline)
        options.signal?.removeEventListener("abort", cancel)
        cancellers.delete(cancel)
        try { proc?.kill() } catch { /* already exited */ }
      }
    }
  }

  return {
    run: at(cwd),
    at,
    dispose() {
      disposed = true
      for (const cancel of cancellers) cancel()
    },
  }
}
