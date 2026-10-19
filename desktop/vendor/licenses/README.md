# Bundled dependency license supplements

These exact upstream license texts cover npm releases whose tarballs omit their
license file. The build plugin pins each supplement to a package version, so an
upgrade requires checking its license again. Remove a fallback when the published
package includes its own license. These files are included in the renderer's
`THIRD-PARTY-LICENSES.txt`; they are not runtime dependencies.

- `remark-math-MIT.txt`: identical license texts verified at both release commits:
  - remark-math 6.0.0 (tag `6.0.0`):
    https://github.com/remarkjs/remark-math/blob/d5d0660b150810a535bbb07eac6cc96a4510aa24/license
  - rehype-katex 7.0.1 (tag `rehype-katex@7.0.1`):
    https://github.com/remarkjs/remark-math/blob/88a9497e1ede93b958237c85edbf5651faeca7af/license
- `cordis-MIT.txt`: cordis 4.0.0-rc.8 (tag `v4.0.0-rc.8`):
  https://github.com/cordiverse/cordis/blob/f46ae95e039f156b966e1e0f7e8d1af91e73e9db/LICENSE
