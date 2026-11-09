package titleservice

import (
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const MaxRuneLength = 40

// threadTitleSystemPrompt is adapted from OpenCode's title prompt, with
// shorter, language-aware targets for Wuu's conversation sidebar. The hard
// character limit is supplied from MaxRuneLength when building the request.
const threadTitleSystemPrompt = `You are a title generator. Output only a short conversation title.

<task>
Create a compact sidebar label that helps the user recognize this conversation.
Return one plain-text line, with no quotes, Markdown, prefix, or explanation.
The title must be at most %d characters, including spaces and punctuation.
</task>

<rules>
- Use the user's language and natural phrasing.
- For Chinese, aim for 8–14 characters. For English, aim for 3–6 words. Shorter is fine when the topic is clear; never pad a title to reach a target.
- Capture one main topic or intent. Use a short phrase, not a sentence summarizing the whole request.
- Put the distinguishing subject first or near the start. Omit background, motivation, implementation steps, secondary tasks, and expected outcomes.
- Do not add a subtitle or join several goals into a list.
- Keep only technical terms, filenames, numbers, or error codes needed to identify the topic, and spell those exactly. Omit paths and incidental details.
- When a file is mentioned, name the intended change or question, not merely the file sharing.
- Omit conversational filler such as "please help me", "帮我看看", and "关于". Avoid generic wrappers such as "分析与优化" or "方案讨论" unless they are the topic.
- Do not invent a tech stack or a solution, answer the request, or use tools.
- For greetings or minimal input, use a brief label matching the intent, such as "Greeting" or "打个招呼".
- Before responding, shorten the title until only the core topic and necessary distinguishing details remain.
</rules>

<examples>
"debug 500 errors in production" → Production 500 errors
"refactor user service" → Refactor user service
"why is app.js failing" → app.js failure
"@src/auth.ts can you add refresh token support" → Auth refresh tokens
"@App.tsx add dark mode toggle" → Dark mode toggle
"帮我排查登录后页面崩溃的问题，看看是不是认证流程出了问题，再给个修复方案" → 排查登录页面崩溃
"我们生成标题的链路能不能优化一下，现在的标题感觉太长了" → 优化会话标题长度
"帮我看看侧边栏的间距、对齐和选中状态，调整一下让它看起来协调一些" → 调整侧边栏布局
"React useEffect 为什么会执行两次，应该怎么处理" → useEffect 重复执行
"检查 PostgreSQL 连接池的配置，最近高峰期经常超时，顺便看看日志" → PostgreSQL 连接超时
</examples>`

func RecommendedTemperature(modelID string) (float64, bool) {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return 0, false
	}
	if strings.Contains(id, "qwen") {
		return 0.55, true
	}
	if strings.Contains(id, "claude") {
		// Claude rejects temperature when reasoning is enabled and tolerates
		// the default otherwise; opencode opts to never send one.
		return 0, false
	}
	if strings.Contains(id, "gemini") {
		return 1.0, true
	}
	if strings.Contains(id, "glm-4.6") || strings.Contains(id, "glm-4.7") {
		return 1.0, true
	}
	if strings.Contains(id, "minimax-m2") {
		return 1.0, true
	}
	if strings.Contains(id, "kimi-k2") {
		// kimi-k2-thinking, kimi-k2.5/.6/.7/.8, kimi-k2p5, kimi-k2-5 all want 1.0.
		for _, marker := range []string{"thinking", "k2.", "k2p", "k2-5"} {
			if strings.Contains(id, marker) {
				return 1.0, true
			}
		}
		return 0.6, true
	}
	return 0, false
}

func FirstHumanMessage(messages []string, force bool) (string, bool) {
	var first string
	count := 0
	for _, message := range messages {
		if content := strings.TrimSpace(message); content != "" {
			count++
			if count == 1 {
				first = content
			}
		}
	}
	return first, first != "" && (force || count == 1)
}
func BuildPlan(in PlanRequest) Plan {
	if in.Persist && strings.TrimSpace(in.Title) != "" {
		return Plan{Action: "skip", Reason: "session already titled"}
	}
	if in.ReadOnly || in.Ephemeral || in.ParentID != "" {
		return Plan{Action: "skip", Reason: "thread read-only / ephemeral / forked"}
	}
	first, ok := FirstHumanMessage(in.HumanMessages, in.Force)
	if strings.TrimSpace(in.Summary) != "" {
		first = strings.TrimSpace(in.Summary)
		ok = true
	}
	if !ok {
		return Plan{Action: "skip", Reason: "no eligible first user message"}
	}
	if in.Model == "" {
		return Plan{Action: "skip", Reason: "no model resolved"}
	}
	plan := Plan{Action: "infer", Source: first, Messages: []pluginprotocol.ModelMessageV1{
		{Role: "system", Content: fmt.Sprintf(threadTitleSystemPrompt, MaxRuneLength)},
		{Role: "user", Content: "Generate a title for this conversation:\n" + first},
	}}
	if temperature, send := RecommendedTemperature(in.Model); send {
		plan.Temperature = &temperature
	}
	return plan
}

// cleanGeneratedThreadTitle strips reasoning fences, optional list/title
// prefixes, and surrounding quotes before applying the generated-title limit.
func Clean(text string) string {
	text = providers.StripThinkingText(text)
	for _, line := range strings.Split(text, "\n") {
		title := strings.TrimSpace(line)
		// Bullet prefix.
		title = strings.TrimPrefix(title, "- ")
		title = strings.TrimPrefix(title, "* ")
		// English "Title:" prefix (model habit).
		for _, prefix := range []string{"Title:", "title:", "TITLE:"} {
			if strings.HasPrefix(title, prefix) {
				title = strings.TrimSpace(strings.TrimPrefix(title, prefix))
			}
		}
		// Chinese 标题: / 标题: prefixes (half-width + full-width colon).
		for _, prefix := range []string{"标题:", "标题:", "题目:", "题目:"} {
			if strings.HasPrefix(title, prefix) {
				title = strings.TrimSpace(strings.TrimPrefix(title, prefix))
			}
		}
		title = strings.Trim(strings.TrimSpace(title), "\"'`“”‘’《》「」『』")
		if title == "" {
			continue
		}
		runes := []rune(title)
		if len(runes) > MaxRuneLength {
			title = string(runes[:MaxRuneLength])
		}
		return strings.TrimSpace(title)
	}
	return ""
}
