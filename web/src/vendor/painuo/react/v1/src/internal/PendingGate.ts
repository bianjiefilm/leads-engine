export interface PendingGate { tryEnter(): boolean; leave(): void; readonly pending: boolean }
/** Instance-local synchronous admission; React state is only its visual projection. */
export function createPendingGate(): PendingGate {
 let pending = false;
 return { tryEnter() { if (pending) return false; pending = true; return true; }, leave() { pending = false; }, get pending() { return pending; } };
}
