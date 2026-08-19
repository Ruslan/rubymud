// Read a failed response exactly once. Mapper validation endpoints return a
// JSON envelope, while proxies/auth failures may still return plain text.
export async function readResponseError(response: Response, fallback: string): Promise<string> {
  const text = await response.text();
  if (!text.trim()) return fallback;
  try {
    const envelope: unknown = JSON.parse(text);
    if (envelope && typeof envelope === 'object' && 'error' in envelope &&
      typeof envelope.error === 'string' && envelope.error.trim()) return envelope.error;
    return fallback;
  } catch {
    return text;
  }
}
