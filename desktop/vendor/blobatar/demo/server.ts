import { serve } from "bun";
import index from "./index.html";

const server = serve({
  // 3001, off the well-known 3000, so the grid can run next to anything else.
  port: 3001,
  routes: { "/*": index },
  development: process.env.NODE_ENV !== "production" && { hmr: true, console: true },
});

console.log(`Wuu mascot workbench → ${server.url}`);
