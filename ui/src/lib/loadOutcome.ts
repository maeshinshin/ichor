// A load that failed is not an empty result: it returns null and the error, so a page shows the error
// instead of a false zero. Pure, so it runs under `node --test`.
export async function loadOutcome<T>(load: () => Promise<T>): Promise<{ value: T | null; error: unknown }> {
  try {
    return { value: await load(), error: null }
  } catch (error) {
    return { value: null, error }
  }
}
