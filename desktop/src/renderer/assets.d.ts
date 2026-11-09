// Static assets imported by the renderer resolve to their bundled URL
// (electron-vite / Vite asset handling).
declare module "*.png" {
  const url: string;
  export default url;
}
