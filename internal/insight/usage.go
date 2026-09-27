// Package insight aggregates session-level token usage and model breakdowns
// for the desktop settings page (appserver handleSettingsUsage). The public
// surface is ScanSessions, CollectUsageScan, and their row/meta types.
//
// Earlier code was removed as dead code and lives only in git history:
//   - CollectTokenUsageRows, replaced by session.ListTokenUsage, which reads
//     token_usage rows without loading conversation content;
//   - UsageReport/BuildUsageReport/FormatUsageReport, a text-format summary
//     superseded by the SettingsUsageResponse RPC;
//   - the LLM-driven insights report (Run, facet extraction via ExtractFacet,
//     GenerateInsights, GenerateHTML, and the usage-data cache), which never
//     had a production trigger.
package insight
