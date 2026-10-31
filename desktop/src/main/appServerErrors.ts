// Distinguishes an explicit protocol rejection from uncertain transport failure.
export class AppServerResponseError extends Error {
  constructor(message: string, readonly code?: string) { super(message); }
}
