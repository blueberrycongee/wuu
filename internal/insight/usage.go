// Package insight aggregates session-level token usage and model breakdowns
// for the desktop settings page (appserver handleSettingsUsage). The public
// surface is CollectUsageScan and its row/skill types.
//
// Earlier code was removed as dead code and lives only in git history:
//   - ScanSessions/CollectSkillUsage and their conversation-content scans,
//     which had no production caller once settings usage moved to the
//     single-pass CollectUsageScan;
//   - CollectTokenUsageRows, replaced by session.ListTokenUsage, which reads
//     token_usage rows without loading conversation content;
//   - UsageReport/BuildUsageReport/FormatUsageReport, a text-format summary
//     superseded by the SettingsUsageResponse RPC;
//   - the LLM-driven insights report (Run, facet extraction via ExtractFacet,
//     GenerateInsights, GenerateHTML, and the usage-data cache), which never
//     had a production trigger.
package insight
