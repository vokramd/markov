# Bug: whitespace around an exact expression, and around set_fact strings, is dropped

## Status

Fixed 2026-11-06.

- `renderValue` (`pkg/template/template.go`) puts the whitespace around an exact `{{ expr }}`
  back when the value is a string (`withSurroundingSpace`). Lists, maps and numbers stay native
  without it, so a block scalar holding `{{ items }}` still yields a list.
- `coerceString` (`pkg/engine/facts.go`) still ignores surrounding whitespace when reading a
  bool, number or JSON, but returns an ordinary string as written, matching what
  `CoerceString`'s doc already said. It is used for `set_fact` values, call `vars:` and `--var`.
- Docs: `template-engine.md` (exact expressions, Type Coercion).

Tests:
- `TestExactExpressionKeepsSurroundingWhitespaceForStrings` (pkg/template). It failed for
  `"{{ s }}\n"`, `"  {{ s }}"` and `"{{ obj | to_json }}\n"`.
- `TestSetFactAndCallVarsKeepATrailingNewline` (pkg/engine). It failed with `line|length == 6`:
  the newline was trimmed.

## Summary

`exactExpression` trims the template before checking that it is a single `{{ }}`, so
`"{{ x | to_json }}\n"` counted as exact and came back as the bare value. Separately,
`coerceString` trimmed every rendered string and returned the trimmed text even when it wasn't a
number, bool or JSON.

## Reproduction

markov-harness appends JSONL lines with `write_file` and
`content: "{{ message | to_json }}\n"`. Each line lost its newline, so lines ran together. The
harness worked around it with a `nl: "\n"` var: `"{{ message | to_json }}{{ nl }}"` is not a
single expression. Its README records it under "Markov findings".

## Behavior change

A `--var` value, a `set_fact` string or a call var with leading or trailing spaces now keeps
them. Bools, numbers and JSON read the same as before.
