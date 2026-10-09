// Package pluginsdk supplies the public TypeScript SDK source to Wuu's local
// development loader. The loader runs this same source, so its transport and
// lifecycle behavior cannot drift from the SDK published to package authors.
package pluginsdk

import "embed"

// Sources contains the SDK and its generated public theme contract.
//
//go:embed src/index.ts src/theme-contract.generated.ts src/bundle-contract.ts
var Sources embed.FS
