import type {ProductComposition} from '@wuu/plugin-sdk';

// Ordinary ProductComposition selection for the config.json entry point.
// Providers and their dependencies are explicit; installation still grants trust.
export const composition = {
  version: 4,
  profile: 'minimal',
  patches: [{
    modules: {'example.echo-engine': true},
    services: [{service: 'engine.wuu', major: 1, provider: 'example.echo-engine'}],
  }],
} satisfies ProductComposition;

export const config = {composition, skills: {enabled: false}};
