package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/config"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/instructions"
	"github.com/blueberrycongee/wuu/internal/prompt"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/tools"
	"strings"
)

// Legacy fixture builders remain test-only; production uses agent.prompt.
func systemPromptForThreadRoot(promptText string, sections []agent.SystemPromptSectionInfo, rootDir, sessionDate string) (string, []agent.SystemPromptSectionInfo) {
	envSection := environmentSystemPromptSection(rootDir, sessionDate)
	if strings.TrimSpace(promptText) == "" || strings.TrimSpace(envSection) == "" {
		return promptText, append([]agent.SystemPromptSectionInfo(nil), sections...)
	}
	const marker = "# Environment"
	start := strings.Index(promptText, marker)
	if start < 0 {
		return promptText, append([]agent.SystemPromptSectionInfo(nil), sections...)
	}
	end := len(promptText)
	if next := strings.Index(promptText[start+len(marker):], "\n\n# "); next >= 0 {
		end = start + len(marker) + next
	}
	// Section metadata is authoritative when valid: an appended plugin section
	// need not start with a Markdown heading and must not be swallowed here.
	for _, section := range sections {
		if section.Key != "environment" || section.Bytes <= 0 || section.Bytes > len(promptText)-start {
			continue
		}
		boundary := start + section.Bytes
		sum := sha256.Sum256([]byte(promptText[start:boundary]))
		if section.Hash == hex.EncodeToString(sum[:16]) {
			end = boundary
			break
		}
	}
	updated := promptText[:start] + envSection + promptText[end:]
	return updated, updateEnvironmentSectionInfo(sections, envSection)
}

func updateEnvironmentSectionInfo(sections []agent.SystemPromptSectionInfo, envSection string) []agent.SystemPromptSectionInfo {
	return updateSectionInfo(sections, "environment", envSection)
}

func updateSectionInfo(sections []agent.SystemPromptSectionInfo, key, content string) []agent.SystemPromptSectionInfo {
	out := append([]agent.SystemPromptSectionInfo(nil), sections...)
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:16])
	for i := range out {
		if out[i].Key == key {
			out[i].Bytes = len([]byte(content))
			out[i].Hash = hash
			return out
		}
	}
	return out
}

// buildWorkerBasePrompt assembles a worker subagent's base system prompt.
// Workers get the READ-ONLY user-notebook variant (memory-redesign §3:
// 临时 worker 只读注入): the user index is injected as orientation — read
// fresh here because worker/thread creation is a prompt-prefix creation
// moment under the cache red lines — while the notebook directory stays
// outside the worker's writable file scope.
func buildWorkerBasePrompt(rootDir, sessionDate, userPrompt, providerName, model string, toolSurface capability.Surface, instructionFiles []instructions.File, discoveredSkills []skills.Skill) string {
	return buildBaseSystemPromptContent(rootDir, sessionDate, config.WorkerSystemPrompt(), userPrompt, providerName, model, toolSurface, instructionFiles, "", "", discoveredSkills)
}

func assemblePluginSystemPrompt(base string, assembler *agent.SystemPromptAssembler) (string, []agent.SystemPromptSectionInfo) {
	if assembler == nil {
		return base, nil
	}
	pluginText, sections := assembler.Assemble("")
	if strings.TrimSpace(pluginText) == "" {
		return base, sections
	}
	if strings.TrimSpace(base) == "" {
		return pluginText, sections
	}
	return base + "\n\n" + pluginText, sections
}

// buildBaseSystemPrompt keeps the frozen-date-agnostic signature used by tests
// and standalone callers; it passes an empty sessionDate so the environment
// section falls back to the current date.
func buildBaseSystemPrompt(rootDir, basePrompt, userPrompt, providerName, model string, toolSurface capability.Surface, instructionFiles []instructions.File, memdirTeaching, memdirIndex string, discoveredSkills []skills.Skill) string {
	return buildBaseSystemPromptContent(rootDir, "", basePrompt, userPrompt, providerName, model, toolSurface, instructionFiles, memdirTeaching, memdirIndex, discoveredSkills)
}

func buildBaseSystemPromptContent(rootDir, sessionDate, basePrompt, userPrompt, providerName, model string, toolSurface capability.Surface, instructionFiles []instructions.File, memdirTeaching, memdirIndex string, discoveredSkills []skills.Skill) string {
	return buildBaseSystemPromptResult(rootDir, sessionDate, basePrompt, userPrompt, providerName, model, toolSurface, instructionFiles, memdirTeaching, memdirIndex, discoveredSkills).Content
}

func buildBaseSystemPromptResult(rootDir, sessionDate, basePrompt, userPrompt, providerName, model string, toolSurface capability.Surface, instructionFiles []instructions.File, memdirTeaching, memdirIndex string, discoveredSkills []skills.Skill) prompt.BuildResult {
	var pb prompt.Builder
	pb.AddSection("base", basePrompt, true)
	pb.AddSection("tool_surface", toolSurface.SystemFragment, true)
	if _, ok := toolSurface.Tools["tool_search"]; ok {
		pb.AddSection("deferred_tool_catalog", toolSurface.DeferredToolCatalog, true)
	}
	pb.AddSection("environment", environmentSystemPromptSection(rootDir, sessionDate), true)
	if strings.TrimSpace(userPrompt) != "" {
		pb.AddSection("user_custom_prompt", "# User Custom Instructions\n\nFollow these user-defined instructions unless they conflict with wuu's built-in behavior, safety, or tool-use discipline above.\n\n"+userPrompt, true)
	}
	pb.AddInstructions(instructionFiles)
	if strings.TrimSpace(memdirTeaching) != "" {
		pb.AddMemdir(memdirTeaching, memdirIndex)
	}
	if toolSurface.ProfileName != "" {
		pb.AddSkills(tools.FilterSkillsForSurface(discoveredSkills, toolSurface))
	}
	return pb.BuildWithInfo()
}

// environmentSystemPromptSection renders the static "# Environment" system
// section. The date is the session-start frozen value (sessionDate) so the
// cached system prefix stays byte-stable across turns and thread rebuilds; a
// long-lived session that crosses a day boundary keeps its start date instead
// of busting the prompt cache. Real-time clock reads belong in the per-turn
// message stream, not this cached prefix. When sessionDate is empty (callers
// without a frozen session, e.g. standalone worker-prompt helpers) it falls
// back to the current date.
func environmentSystemPromptSection(rootDir, sessionDate string) string {
	env := wuucontext.Snapshot(rootDir)
	if frozen := strings.TrimSpace(sessionDate); frozen != "" {
		env.Date = frozen
	}
	var b strings.Builder
	b.WriteString("# Environment\n\n")
	if cwd := strings.TrimSpace(env.CWD); cwd != "" {
		fmt.Fprintf(&b, "- Current working directory: %s\n", cwd)
	}
	if date := strings.TrimSpace(env.Date); date != "" {
		fmt.Fprintf(&b, "- Current date: %s\n", date)
	}
	return strings.TrimRight(b.String(), "\n")
}
