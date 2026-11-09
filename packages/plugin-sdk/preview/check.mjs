import ts from 'typescript';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const packageRoot = fileURLToPath(new URL('../', import.meta.url));

export function checkSources(sources, contents) {
  const options = { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext,
    moduleResolution: ts.ModuleResolutionKind.Bundler, jsx: ts.JsxEmit.React,
    strict: true, noEmit: true, skipLibCheck: true };
  const host = ts.createCompilerHost(options);
  const readFile = host.readFile;
  host.readFile = (path) => { const index = sources.indexOf(path); return index !== -1 ? contents[index] : readFile(path); };
  host.resolveModuleNames = (names, containingFile) => names.map((name) => {
    if (name === '@wuu/plugin-sdk' || name === '@wuu/plugin-sdk/ui') {
      return { resolvedFileName: resolve(packageRoot, name.endsWith('/ui') ? 'dist/ui.d.ts' : 'dist/index.d.ts'), extension: ts.Extension.Dts, isExternalLibraryImport: true };
    }
    return ts.resolveModuleName(name, containingFile, options, host).resolvedModule;
  });
  return ts.getPreEmitDiagnostics(ts.createProgram(sources, options, host)).map((diagnostic) => {
    const position = diagnostic.file?.getLineAndCharacterOfPosition(diagnostic.start ?? 0);
    return { message: `TS${diagnostic.code}: ${ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n')}`,
      file: diagnostic.file?.fileName, line: position ? position.line + 1 : undefined,
      column: position ? position.character + 1 : undefined };
  });
}
