// Launch the production desktop entry in an explicit validation data directory.
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { app } = require('electron');
// The development shell may export this globally; validate the built renderer.
delete process.env.ELECTRON_RENDERER_URL;
if (!process.env.WUU_HOME || !path.isAbsolute(process.env.WUU_HOME)) throw new Error('Set WUU_HOME to a dedicated absolute validation directory');
const repo = process.env.WUU_VALIDATION_REPO || path.resolve(__dirname,'../../..');
const userData = path.join(process.env.WUU_HOME,'electron');
fs.mkdirSync(userData,{recursive:true,mode:0o700});
app.setPath('userData',userData);
app.setPath('sessionData',userData);
app.setName('Wuu Mobile Validation');
import(pathToFileURL(path.join(repo,'desktop/out/main/index.js')).href).catch(error=>{ console.error(error);app.exit(1); });
