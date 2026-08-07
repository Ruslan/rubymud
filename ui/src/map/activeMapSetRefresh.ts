import { LatestRequest } from "./latestRequest";

// Only Session.WriteTopology's COW notification carries a positive map_set_id.
// Missing/zero ids are generic invalidations (for example an import) and retain
// the historical reset-to-player behaviour.
export function cowNotificationMapSetID(mapSetID: number | undefined): number | undefined {
  return Number.isSafeInteger(mapSetID) && (mapSetID ?? 0) > 0 ? mapSetID : undefined;
}

export class ActiveMapSetRefresh {
  private latest = new LatestRequest();

  constructor(
    private read: () => Promise<number | null>,
    private current: () => number | null,
    private set: (id: number | null) => void,
    private reload: (preserveView?: boolean) => void,
  ) {}

  async refresh(forceReload = false, preserveForMapSetID?: number): Promise<void> {
    const request = this.latest.begin();
    try {
      const next = await this.read();
      if (!this.latest.isCurrent(request)) return;
      const changed = next !== this.current();
      if (changed) this.set(next);
      if (changed || forceReload)
        this.reload(preserveForMapSetID !== undefined && next === preserveForMapSetID);
    } catch {
      // Without the authoritative active id, a COW hint cannot be distinguished
      // from a later external switch. A normal reload is the conservative fallback.
      if (forceReload && this.latest.isCurrent(request)) this.reload(false);
    }
  }
}
