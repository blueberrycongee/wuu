# Stateful TypeScript Runtime Example

This package proves the public TypeScript runtime can initiate Host Service calls over the same JSON-lines connection used for capabilities.

- `activate` initializes a workspace counter with compare-and-swap.
- `counter.increment` reads and updates that counter from a capability handler.
- `session.start` creates a plugin-private Session and sends one prompt.

Build the SDK first, install this package's dependencies, run `npm run build`, then install the example through the normal Wuu plugin package flow.
