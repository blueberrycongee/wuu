Import a reviewed Wuu shared-session document into a new conversation in this workspace.

The user supplies the shared document as a file path or pasted JSON (a `SharedDocument` v1 object with `version`, `session_id`, and `items`). Do not paste the document's contents into this conversation and do not summarize it first.

1. Obtain the document. If the user gave a file path, read the file and pass its JSON contents to `import_shared_context` as `document`. If they pasted the JSON, pass it through unchanged.
2. Call `import_shared_context`. Pass `intent` only when the user states what the imported conversation should do next; omit it otherwise. Do not pass `provider` or `model` unless the user asks for a specific one — the current session's defaults apply.
3. Report the created conversation name and session id so the user can switch to it. Mention `omitted_items` when the import truncated older items.

If the document fails validation, report the error; do not retry with edited contents.
