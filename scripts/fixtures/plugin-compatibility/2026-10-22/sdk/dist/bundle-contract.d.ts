export declare const BUNDLE_MANIFEST_SCHEMA_VERSION = 2;
export declare const BUNDLE_CONTRACT_VERSION = 1;
export type CanonicalValue = null | boolean | string | number | CanonicalValue[] | {
    [key: string]: CanonicalValue;
};
export interface AgentDeclaration {
    command: string;
    args?: string[];
    env?: Record<string, string>;
}
export interface DesktopDeclaration {
    entry: string;
}
/** A package dependency; omitted version accepts any installed version. */
export interface PluginDependency {
    id: string;
    /** Semantic-version constraint checked by the host during activation. */
    version?: string;
    /** Missing or incompatible optional dependencies do not block activation. */
    optional?: boolean;
}
export interface BundleManifest {
    schema_version: number;
    id: string;
    version: string;
    name?: string;
    description?: string;
    agent?: AgentDeclaration;
    desktop?: DesktopDeclaration;
    requires?: string[];
    dependencies?: PluginDependency[];
}
export interface GenerationInput {
    contract_version: number;
    manifest: CanonicalValue;
    content: Record<string, string>;
}
export declare function validateBundleManifest(manifest: unknown): string[];
export declare function canonicalize(value: CanonicalValue): Uint8Array;
export declare function generation(input: GenerationInput, sha256Hex: (bytes: Uint8Array) => string | Promise<string>): Promise<string>;
