# Task: Workflow subdirectories with qualified names

## Status

Done 2026-11-06.

- **Parser:** `parser.DirWorkflowFiles` (recursive, hidden skipped), and the name check in
  `ParseDir`.
- **Codify:** its name-to-file map uses the same lister.
- **Docs:** "Subfolders and qualified names" in `workflow-file.md`.
- **Tests:**
  - `TestParseDirLoadsSubfoldersByQualifiedName`,
    `TestParseDirRejectsASubfolderNameThatDoesNotMatchItsPath`,
    `TestParseDirCatchesADottedFolderCollidingWithANestedOne`,
    `TestParseDirTopLevelFilesKeepTheirOwnNames`;
  - `TestEditableSetFindsAWorkflowInASubfolder`;
  - `make test` passes.
- **markov-harness:** its tools moved to `workflows/tools/` (`tools.bash` … `tools.unknown`,
  dispatched as `tools.{{ block.name }}`), and all 16 end-to-end tests pass.
- **markov-ide:** its task `workflow-subfolders`.

## Why

A directory workflow's `workflows/` is read flat, and subfolders are skipped without a word:
`workflows/tools/grep.yaml` simply doesn't exist to markov ("references unknown workflow"). A
workflow with many small workflows (markov-harness has seven `tool_*` files) can't group them.
Dotted names already work (`name: tools.grep` in `workflows/tools.grep.yaml` validates and can be
called), so the missing piece is loading subfolders.

## Design

- **Loading:** `workflows/` is read recursively. Hidden files and folders (starting with `.`) are
  skipped.
- **Names:** a file in a subfolder is named by its path, with `/` read as `.`:
  `workflows/tools/grep.yaml` is `tools.grep`. Its `name:` may be left out, and then it is that
  name. If it is given, it must equal it; a mismatch is an error naming the expected name.
- **Top-level files** keep today's rule: `name:` is required and the filename is only
  organizational.
- **Calls** use the full name (`workflow: tools.grep`, or templated
  `workflow: "tools.{{ name }}"`). No relative names, per-folder vars or private workflows: that
  would be a module system, which nothing needs yet.
- **Collisions** (`workflows/a.b/c.yaml` and `workflows/a/b/c.yaml`) are caught by the existing
  duplicate-name check.
- **One lister for every consumer:** `parser.DirWorkflowFiles` lists the files with their
  qualified names, for `ParseDir` and for codify's name-to-file map, so they can't disagree.

## Verification

- Parser tests: nested load; a name derived from the path; a mismatched name is rejected; hidden
  folders skipped; top-level unchanged; duplicate via dotted folder.
- Codify finds a nested workflow's file.
- markov-harness: `tool_*` become `workflows/tools/*.yaml` (`tools.bash` …), and its suite passes.
- markov-ide reads nested workflow files.
