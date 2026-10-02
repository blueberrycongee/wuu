// Typography: families, scale, mixed Chinese/Latin setting, specimens.
import { page, title, ball, lockup, C, T, staticBall, agentBall } from "../kit.mjs";

const S = T.type.scale;

export const families = page({
  chapter: 4, id: "type-families",
  body: `
  <div class="grid">
    <div class="span-4">${title("字体", "Typefaces")}
      <p class="lead">品牌字体见右表。产品界面跟随系统与用户设置。</p>

    </div>
    <div class="span-7 start-6 stack" style="--s:0px">
      ${[
        ["Hanken Grotesk", "拉丁字母 · 标题与正文", `font-family:'Hanken Grotesk'`, "Review every change", "Aa Bb Gg 0123456789 → ✓", "400 · 500 · 600（可变字重 100–900，只用正体）", "SIL OFL 1.1 · 随仓库分发 brand/fonts/hanken-grotesk"],
        ["思源黑体 · Source Han Sans SC", "中文 · 标题与正文", `font-family:'Source Han Sans SC','Noto Sans SC','Noto Sans CJK SC'`, "每一处改动，都看得见", "永 东 國 设置 模型服务 会话", "Regular 400 · Medium 500 · Bold 700", "SIL OFL 1.1 · 体积大，不随仓库分发；从 Adobe 或 Google 官方发布获取"],
        ["Fragment Mono", "等宽 · 命令、变量、数值标签", `font-family:'Fragment Mono'`, "wuu exec --json", "0O 1lI {} [] => != ~/.wuu", "Regular 400；关闭连字", "SIL OFL 1.1 · 随仓库分发 brand/fonts/fragment-mono"],
        ["系统界面字体", "产品界面", `font-family:${T.type.family.product.replace(/"/g, "'")}`, "设置 · Settings", "SF Pro、PingFang SC、Segoe UI、微软雅黑", "跟随系统与用户设置", "操作系统自带；代码区使用用户选择的等宽字体"],
      ].map(([n, role, ff, big, glyphs, weights, lic]) => `
      <div class="row" style="--g:32px;--a:flex-start;padding:22px 0;border-bottom:1px solid var(--line-1)">
        <div style="width:230px"><div class="h4">${n}</div><div class="cap" style="margin-top:2px">${role}</div><div class="cap" style="margin-top:12px;font-weight:400">${weights}</div></div>
        <div style="flex:1"><div style="${ff};font-size:36px;line-height:1.15;font-weight:600;letter-spacing:-.01em;color:var(--text)">${big}</div><div style="${ff};font-size:17px;margin-top:8px;color:var(--text-2)">${glyphs}</div><div class="cap" style="margin-top:10px">${lic}</div></div>
      </div>`).join("")}
    </div>
  </div>`,
});

export const scale = page({
  chapter: 4, id: "type-scale",
  body: `
  <div class="grid">
    <div class="span-3">${title("字号层级", "Type scale")}
      <p class="lead">用于官网、文档、社交图和物料。层级靠字号和字重拉开，同一版面最多用四级。</p>
      <p class="body" style="margin-top:20px">中文标题用 700 字重，不加负字距；拉丁字母标题用 600。</p>
      <p class="body" style="margin-top:14px">产品界面与代码字号独立设置；默认值以产品设计系统为准。见 <span class="mono">docs/en/project/design-system.md</span>。</p>
    </div>
    <div class="span-9 start-4">
      <table class="spec" style="table-layout:fixed">
        <thead><tr><th style="width:100px">层级</th><th style="width:150px">字号 / 行高 / 字重</th><th>示例</th></tr></thead>
        <tbody>
        ${Object.entries(S).map(([k, v]) => `<tr><td><span class="h4" style="text-transform:capitalize">${k}</span></td><td class="num">${v.size} / ${v.line} / ${v.weight}${v.tracking ? `<br><span class="cap">字距 ${v.tracking} em</span>` : ""}</td>
          <td style="padding:14px 0"><div style="font:${v.weight} ${Math.min(v.size, 72)}px/${v.line} var(--brand);letter-spacing:${v.tracking}em;color:var(--text);white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${{ display: "和 agent 一起把事做完", headline: "每一处改动都留给你检查", title: "连接模型服务 Model services", subtitle: "选择一个本地文件夹作为工作区", body: "Wuu 在权限模式内读取和修改文件、运行命令。提示词与相关上下文会发送给你选择的模型服务。", small: "会话与设置默认保存在 ~/.wuu。", caption: "更新于 2026.9.29 · 3 分钟阅读" }[k]}</div></td></tr>`).join("")}
        </tbody>
      </table>
    </div>
  </div>`,
});

const ex = (ok, zh, note) => `<div class="row" style="--g:12px;--a:baseline;padding:9px 0;border-top:1px solid var(--line-1)"><span class="verdict ${ok ? "yes" : "no"}" style="width:16px">${ok ? "✓" : "✕"}</span><span class="body" style="flex:1;color:var(--text)">${zh}</span><span class="cap" style="width:190px">${note}</span></div>`;

export const mixed = page({
  chapter: 4, id: "type-mixed",
  body: `
  <div class="grid">
    <div class="span-4">${title("中英混排", "Setting Chinese with Latin")}
      <p class="lead">中英文与数字之间留空格；命令和路径用等宽字体。</p>
      <table class="spec" style="margin-top:32px">
        <thead><tr><th>项目</th><th>中文</th><th>英文</th></tr></thead>
        <tbody>
          <tr><td>正文行高</td><td class="num">1.7</td><td class="num">1.6</td></tr>
          <tr><td>理想行长</td><td class="num">28–40 字</td><td class="num">60–75 字符</td></tr>
          <tr><td>段间距</td><td>0.8 行</td><td>0.8 行</td></tr>
          <tr><td>强调</td><td>字重 600/700</td><td>字重 600，不用斜体</td></tr>
          <tr><td>标题字距</td><td>0</td><td>−0.01 至 −0.02 em</td></tr>
        </tbody>
      </table>
    </div>
    <div class="span-7 start-6">
      <div class="label" style="margin-bottom:6px">空格</div>
      ${ex(true, "在 Wuu 里打开 3 个项目，占用 20 MB。", "中英文、数字与单位之间留空格")}
      ${ex(false, "在Wuu里打开3个项目，占用20MB。", "缺少空格")}
      <div class="label" style="margin:26px 0 6px">标点</div>
      ${ex(true, "运行 <code>wuu exec</code> 后，查看输出。", "中文句子用全角标点；命令保持原样")}
      ${ex(false, "运行 wuu exec 后,查看输出.", "中文里混用半角逗号和句号")}
      ${ex(true, "“权限模式”决定 Wuu 能做什么。", "中文用弯引号“”，不用「」混搭")}
      <div class="label" style="margin:26px 0 6px">代码与路径</div>
      ${ex(true, "配置文件在 <code>~/.wuu/config.toml</code>。", "路径、命令、变量名用等宽字体")}
      ${ex(false, "配置文件在 ~/.wuu/config.toml。", "正文字体里的路径难以辨认边界")}
      <div class="label" style="margin:26px 0 6px">数字</div>
      ${ex(true, "<span class=\"num\">已用 42 秒 · 114.1M Token · 3 个文件</span>", "表格和统计使用等宽数字")}
      ${ex(false, "已用四十二秒", "界面与数据中不用中文数字")}
    </div>
  </div>`,
});

export const specimen = page({
  chapter: 4, id: "type-specimen",
  body: `
  <div class="grid">
    <div class="span-3">${title("排版示例", "In use")}
      <p class="lead">长文、密集表格和短标签用同一套规则，只改变字号和间距。</p>
    </div>
    <div class="span-5 start-4">
      <div class="label">长文 · 文档页</div>
      <div class="plate" style="padding:36px 40px;margin-top:12px;height:640px;overflow:hidden">
        <div class="cap">快速开始 · 3 分钟</div>
        <div style="font:700 30px/1.25 var(--brand);margin-top:10px;letter-spacing:-.005em">完成第一个任务</div>
        <p style="font:400 16px/1.75 var(--brand);color:var(--text-2);margin-top:16px">连接模型服务，选择工作区，再输入任务。只需了解项目时，可用只读模式。</p>
        <p style="font:400 16px/1.75 var(--brand);color:var(--text-2);margin-top:13px">只读运行：</p>
        <div class="codeblock" style="background:var(--surface-2);border-radius:10px;padding:14px 16px;margin-top:12px;font-size:13.5px">wuu exec --permission-mode read_only \\<br>&nbsp;&nbsp;"review this project and explain how to run its tests"</div>
        <div style="font:700 19px/1.4 var(--brand);margin-top:26px">检查改动</div>
        <p style="font:400 16px/1.75 var(--brand);color:var(--text-2);margin-top:8px">任务结束后，在右侧“改动”面板查看 diff。</p>
        <p style="font:400 15px/1.65 var(--brand);color:var(--text-2);margin-top:18px;border-top:1px solid var(--line-1);padding-top:16px">Connect a model provider, then choose a local folder. Wuu reads files, edits code and runs commands within the active permission mode.</p>
      </div>
    </div>
    <div class="span-4 start-9">
      <div class="label">密集表格 · 设置</div>
      <div class="plate" style="padding:20px 24px;margin-top:12px">
        <table class="spec" style="font-size:13px">
          <thead><tr><th>模型</th><th>服务</th><th style="text-align:right">上下文</th></tr></thead>
          <tbody>
            ${[["sky", "claude-sonnet-5-5", "Anthropic", "1M"], ["peach", "gpt-5", "OpenAI", "400K"], ["leaf", "qwen3-coder", "阿里云", "256K"], ["iris", "deepseek-v4", "DeepSeek", "128K"]].map(([a, m, p, c]) => `<tr><td><div class="row" style="--g:8px">${agentBall(16, a)}<span class="mono" style="font-size:12.5px">${m}</span></div></td><td>${p}</td><td class="num" style="text-align:right">${c}</td></tr>`).join("")}
          </tbody>
        </table>
      </div>
      <div class="label" style="margin-top:28px">短标签 · 按钮与菜单</div>
      <div class="plate" style="padding:22px 24px;margin-top:12px">
        <div class="row" style="--g:10px;flex-wrap:wrap">
          <span style="background:var(--text);color:var(--canvas);border-radius:999px;padding:8px 16px;font:500 13.5px var(--brand)">开始任务</span>
          <span style="box-shadow:inset 0 0 0 1px var(--boundary);border-radius:999px;padding:8px 16px;font:500 13.5px var(--brand)">查看改动</span>
          <span style="border-radius:999px;padding:8px 12px;font:500 13.5px var(--brand);color:var(--text-2)">稍后</span>
        </div>
        <div style="margin-top:18px;box-shadow:0 0 0 1px var(--line-1),0 8px 24px rgba(20,20,17,.08);border-radius:12px;padding:6px;width:240px">
          ${["重命名会话", "移到文件夹", "复制会话链接"].map((t, i) => `<div style="padding:7px 10px;border-radius:7px;font:500 13px var(--brand);${i === 0 ? "background:var(--surface-3)" : ""}">${t}</div>`).join("")}
          <div style="height:1px;background:var(--line-1);margin:4px 6px"></div>
          <div style="padding:7px 10px;font:500 13px var(--brand);color:${C.status.danger.light}">删除会话</div>
        </div>
      </div>
      <p class="cap" style="margin-top:12px">示例数据为虚构。</p>
    </div>
  </div>`,
});

export default [families, scale, mixed, specimen];
