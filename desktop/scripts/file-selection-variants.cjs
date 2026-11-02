// Shared by the unchanged single-process harness and the two-process CI join.
const variants = Object.freeze([
  { name: 'light-default-wide', theme: 'light', size: 14, width: 1380, height: 900 },
  { name: 'dark-large-wide', theme: 'dark', size: 20, width: 1380, height: 900 },
  { name: 'light-large-narrow', theme: 'light', size: 20, width: 760, height: 900 },
  { name: 'dark-default-narrow', theme: 'dark', size: 14, width: 760, height: 900 },
].map(Object.freeze));
const caseKinds = Object.freeze(['selection-guide.md', 'selection-code.ts', 'selection-notes.txt',
  'text-attachment', 'selection-wrapped.ts', 'mixed-pdf-text-history']);
function selectVariants(shard) {
  if (shard === undefined) return variants;
  if (shard !== '1/2' && shard !== '2/2') throw new Error(`Unknown file-selection shard: ${JSON.stringify(shard)}`);
  return variants.filter((_, index) => index % 2 === Number(shard[0]) - 1);
}
module.exports = { variants, caseKinds, selectVariants };
