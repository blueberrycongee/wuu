// Package pluginsdk supplies the public TypeScript SDK source to Wuu's local
// development loader. The loader runs this same source, so its transport and
// lifecycle behavior cannot drift from the SDK published to package authors.
package pluginsdk

import "embed"

// Sources contains the SDK and its generated public theme contract.
//
//go:embed src/client.ts src/execution-authorization.ts src/personal-memory.ts src/personal-authority.ts
//go:embed src/session-transport.ts src/desktop-session-transport.ts src/session-sharing.ts src/identity.ts src/resource-policy.ts
//go:embed src/model-capacity.ts src/experiment.ts src/agent-policy.ts src/ui-contributions.ts src/engine-admin.ts src/engine.ts
//go:embed src/execution-backend.ts
//go:embed src/index.ts src/theme-contract.generated.ts src/bundle-contract.ts src/model.ts src/process.ts src/settings.ts src/driver.ts src/persistence.ts src/service-delegation.ts src/goal.ts src/ui.ts src/providers.ts src/coordination.ts src/composition.ts
//go:embed src/application-settings.ts src/session-service.ts src/prompt-service.ts src/title-service.ts src/tool-service.ts src/tool-batch.ts src/compaction-service.ts
//go:embed src/ui-desktop.ts src/ui-environment.ts src/ui-onboarding.ts src/ui-composer-runtime.ts src/ui-browser.ts src/ui-composer-presentation.ts src/ui-projects.ts src/ui-submission.ts src/ui-search.ts src/ui-web-workspace.ts src/ui-terminals.ts src/ui-conversation-actions.ts src/ui-settings-controls.ts src/ui-artifacts.ts src/ui-file-selection.ts src/ui-composer.ts src/ui-preview.ts src/ui-navigation.ts src/ui-catalog.ts src/ui-side-conversation.ts src/ui-preview-content.ts src/ui-conversation.ts
var Sources embed.FS
