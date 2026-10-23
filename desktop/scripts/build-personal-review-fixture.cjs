const { build } = require("esbuild");
const fs = require("node:fs");
const path = require("node:path");
const root = path.resolve(__dirname, "..");
const output = path.join(root, "out/personal-review-fixture");
const extra = `html,body,#root{margin:0;min-height:100%;background:var(--paper);color:var(--ink);font-size:var(--font-ui)}*{box-sizing:border-box}.review-fixture-label{display:flex;flex-wrap:wrap;justify-content:space-between;gap:12px;padding:12px 24px;border-bottom:1px solid var(--hairline);color:var(--ink-muted);line-height:1.5}.review-fixture-label a{color:inherit}.review-fixture-main{max-width:780px;padding:32px;margin:0 auto;display:flex;flex-direction:column;gap:32px;overflow:visible!important}.review-fixture-path{margin:0;color:var(--ink-muted)}.review-fixture-main>h1{margin:0;font-size:1.6em;font-weight:600}.review-fixture-main p{line-height:1.6}.review-fixture-main .plugin-ui-section-header h2{font-size:1.13em!important}.review-fixture-main .plugin-ui-section-header p{font-size:1em!important}@media(max-width:480px){.review-fixture-main{padding:24px 16px}.review-fixture-label{padding:12px 16px}}`;
(async()=>{
  fs.mkdirSync(output,{recursive:true});
  const result=await build({entryPoints:[path.join(root,"dev/personal-review/index.tsx")],bundle:true,write:false,outdir:output,format:"iife",platform:"browser",target:"chrome120",define:{"process.env.NODE_ENV":'"production"'},loader:{".woff":"dataurl",".woff2":"dataurl",".ttf":"dataurl"}});
  let js="",css="";for(const file of result.outputFiles){if(file.path.endsWith(".js"))js+=file.text;if(file.path.endsWith(".css"))css+=file.text}
  fs.writeFileSync(path.join(output,"index.html"),`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Twin action approvals · synthetic fixture</title><style>${css}\n${extra}</style></head><body><div id="root"></div><script>${js.replace(/<\/script/gi,"<\\/script")}</script></body></html>`);
  console.log(path.join(output,"index.html"));
})().catch(error=>{console.error(error);process.exitCode=1});
