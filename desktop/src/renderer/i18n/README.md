# Desktop i18n maintenance

This directory is maintained primarily through coding agents. Keep the rules
executable: do not rely on a reviewer noticing a missing locale or placeholder.

## Change a user-facing string

1. Reuse an existing semantic key when the meaning is the same.
2. Otherwise add the key to `resources/zh-CN.ts` and `resources/en-US.ts` in the
   same position. Use `domain.camelCaseName`; do not use English copy as a key.
3. Keep placeholders identical across locales, including case.
4. Run the focused i18n tests and desktop typecheck.

Renderer components use `useI18n().t`. Non-React renderer helpers use
`translateCurrent`. State that must change language after it is created uses
`localizedText` and resolves it only at the display boundary. Native Electron
menus and windows use the smaller catalog in `src/main/i18n.ts`.

Use the i18n number/date helpers for display. Do not call `toLocaleString` with
the system default because the user may select a different Wuu language.

## Voice

Wuu's copy is plain, calm and specific: say what happened and what to do next,
without blame, filler or internal vocabulary.

- **Failures.** Say what could not be done, then the next step when a retry is
  safe: `Couldn’t save. Try again.` Do not open with `Failed to` or `Unable to`,
  and do not add `Please`. Keep an `{error}` or `{reason}` placeholder when the
  cause helps the user. zh-CN keeps its established forms (`保存失败，请重试。`,
  `无法…，请稍后重试。`, `未能…`).
- **Empty states.** Say what is empty and, only when the same screen has the
  control, how to fill it.
- **One noun per object.** A user's own chat is a conversation (对话). Session
  (会话) is reserved for the Project Agent, peer messaging, engine approvals and
  tool history. Do not use both words for one object on one surface.
- **Names, not acronyms.** Write `Programmatic tool calling`, not `PTC`, wherever
  a user reads it or assistive technology announces it.
- **Typography.** Use `’` and `“ ”` rather than `'` and `"`, and the single
  character `…` rather than three dots. zh-CN keeps full-width punctuation.
- **English terms in zh-CN.** Chinese has no plural marker, so a term stays
  singular (`Agent`, not `Agents`) unless it is a product name.

Fix existing strings when you touch them instead of sweeping the catalog: most
`Failed to …` strings are fallbacks that show only when an error carries no
message of its own, so rewording them changes little of what users see.

## Add a locale

1. Add it once to `APP_LOCALES` in `packages/protocol/src/index.ts`.
2. Add its renderer resource and register it in `i18n/index.tsx` and
   `catalogContract.test.ts`.
3. Add its language-picker label in `LanguagePreferenceSection.tsx`.
4. Add its native resource in `src/main/i18n.ts`.
5. Update `resolveAppLocale` in the protocol if the new locale should match an
   OS locale family.

The registry-derived types intentionally make incomplete steps fail typecheck.
`catalogContract.test.ts` then checks runtime invariants that TypeScript cannot:
key order, blank values, and placeholder parity.
