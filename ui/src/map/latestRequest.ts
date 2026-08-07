// Guards a shared async value against late responses from earlier requests.
export class LatestRequest {
  private generation = 0;

  begin(): number {
    return ++this.generation;
  }

  isCurrent(generation: number): boolean {
    return generation === this.generation;
  }
}
