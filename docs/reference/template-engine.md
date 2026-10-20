{% raw %}
# Template Engine Reference

Markov uses [Pongo2 v6](https://github.com/flosch/pongo2/v6) for template rendering -- a Jinja2/Django-compatible template engine for Go. Templates are evaluated at runtime against the workflow context to produce dynamic values for step parameters, conditions, and variable assignments.

## Variable Substitution

```yaml
# Simple value
command: "echo {{ message }}"

# Nested field access
image: "{{ config.registry }}/{{ config.image_name }}"

# Array index access
first_item: "{{ items.0 }}"
also_works: "{{ items[0] }}"
```

| Syntax | Description |
|---|---|
| `{{ variable }}` | Substitute a simple value from context |
| `{{ variable.field }}` | Access a nested map field |
| `{{ array.0 }}` or `{{ array[0] }}` | Access an array element by index |
| `{{ map[key] }}` | Look up a map entry by a variable key; `.field`, `.0` and further `[...]` may follow, as in `{{ tiers[tier].tests }}` |

Values are inserted as they are. Markov turns off Pongo2's HTML autoescaping, because templates
render shell commands, script arguments, JSON bodies and paths, where `&quot;` would corrupt the
value.

## Filters

Filters transform values using the pipe (`|`) operator.

```yaml
# Single filter
greeting: "{{ name | upper }}"

# Filter with argument
label: "{{ description | truncate:50 }}"

# Chained filters
slug: "{{ title | lower | cut:' ' }}"
```

| Syntax | Description |
|---|---|
| `{{ value \| filter }}` | Apply a single filter |
| `{{ value \| filter:arg }}` | Apply a filter with an argument |
| `{{ value \| filter1 \| filter2 }}` | Chain multiple filters left to right |

## Markov Filters

Markov registers additional filters on top of Pongo2's built-in set.

### `trim`

Removes leading and trailing whitespace from a value.

```yaml
- name: set_issue_key
  type: set_fact
  vars:
    issue_key: "{{ command_result.stdout | trim }}"
```

### `csv`

Turns comma-separated text into a list, trimming each item and dropping empty ones. An empty
string gives an empty list, so `default` can supply a fallback. Pongo2's own `split` filter keeps
spaces and empty items.

```yaml
for_each: "only_tests | csv"                 # "S1, S2" -> ["S1", "S2"]
for_each: "variants | csv | default:own"     # "" -> own
```

### `flatten`

Flattens nested lists into one list; `flatten:1` flattens one level only.

```yaml
"{{ [[1, 2], [3]] | flatten }}"     # [1, 2, 3]
```

### `pluck`

Takes one field, which may be a dotted path, from each item of a list. Items without it give
`nil`. With `flatten` it collects results from nested `for_each` registers:

```yaml
stdin: "{{ rounds_done | pluck:'tests_done' | flatten | pluck:'variants_done' | flatten | pluck:'eval.stdout' | join:'\n' }}"
```

### `seq`

Turns a count into the list `1..n`. A numeric string is parsed; an empty value gives an empty list.

```yaml
for_each: "repeats | seq"                    # 3 -> [1, 2, 3]
```

### `from_json` / `fromjson`

Defined in `pkg/template/template.go`. Parses a JSON string into a native Go value (map, list, number, bool, etc.). Whole numbers become integers, so an ID such as `50163277` renders as `50163277` rather than `50163277.000000`; numbers with a fraction or exponent stay floats. The same applies to JSON response bodies from `http_request` and to JSON parsed by `set_fact` and `for_each`.

```yaml
vars:
  parsed_data: "{{ raw_json | from_json }}"
```

`fromjson` is supported as a compatibility alias.

**Input must be valid JSON.** If the input string is not valid JSON, the filter returns an error and the step fails.

### `to_json` / `tojson`

Serializes a value to a compact JSON string. Prefer the Ansible-style `to_json` spelling in new workflows; `tojson` is also supported for Jinja-style compatibility.

```yaml
params:
  command: "echo '{{ config | to_json }}'"
```

Use `to_json` when JSON text is needed inside a larger string. For structured fields such as `http_request.body`, exact value expressions usually do not need this filter because Markov preserves native maps and lists.

```yaml
params:
  body:
    config: "{{ config }}"              # native object
    config_text: "{{ config | to_json }}" # JSON string
```

### Exact value expressions

When a templated field is exactly a single value expression, Markov preserves native maps and lists instead of rendering them through Pongo2's string output:

```yaml
params:
  body:
    args:
      config: "{{ config }}"
```

If `config` is a map or list, the rendered parameter remains a map or list. If `config` is a JSON-looking string beginning with `{` or `[`, Markov parses it into the corresponding native value. This is useful for HTTP request bodies, where the executor JSON-encodes the final body.

Any other expression in that position also keeps its native type: filters, arithmetic and
variable-key lookups. For example:

```yaml
vars:
  tier_def: "{{ tiers[tier] }}"                      # the map, not its printed form
  names: "{{ variants | csv | default:own_names }}"  # a list
  next: "{{ round + 1 }}"                            # an integer
```

An expression that is undefined still renders as the empty string.

This exact-expression path is triggered only when the whole string is the expression, with no surrounding text. Mixed templates still render to strings:

```yaml
params:
  body:
    config: "{{ config }}"                    # native object
    message: "config={{ config | to_json }}"  # string
```

### Special handling in set_fact

When a `set_fact` var value is **exactly** `{{ path | from_json }}` or `{{ path | fromjson }}` (no surrounding text), the engine bypasses normal template rendering. Instead, it:

1. Resolves the dot-path directly from context
2. JSON-parses the raw string value
3. Stores the parsed object/array/value preserving its structure

This avoids the flattening that would occur if pongo2 rendered the value to a string first.

```yaml
# This preserves object structure (direct resolution path)
- name: parse_config
  type: set_fact
  vars:
    config: "{{ raw_config_json | fromjson }}"

# This does NOT get special handling (has surrounding text)
- name: broken
  type: set_fact
  vars:
    config: "prefix {{ raw_config_json | fromjson }} suffix"
```

The special path is triggered only when the entire value matches the pattern `{{ <path> | from_json }}` or `{{ <path> | fromjson }}` with nothing before or after the braces.

## Built-in Pongo2 Filters

These are the most commonly useful filters from Pongo2's built-in set.

| Filter | Description | Example |
|---|---|---|
| `length` | Length of string, list, or map | `{{ items \| length }}` |
| `default` | Fallback if value is empty/nil | `{{ name \| default:"unknown" }}` |
| `upper` | Uppercase string | `{{ status \| upper }}` |
| `lower` | Lowercase string | `{{ name \| lower }}` |
| `join` | Join list elements with separator | `{{ tags \| join:", " }}` |
| `split` | Split string into list | `{{ csv_line \| split:"," }}` |
| `first` | First element of list/string | `{{ items \| first }}` |
| `last` | Last element of list/string | `{{ items \| last }}` |
| `add` | Add a number | `{{ count \| add:1 }}` |
| `truncate` | Truncate string to length | `{{ desc \| truncate:80 }}` |
| `title` | Title-case string | `{{ name \| title }}` |
| `capitalize` | Capitalize first character | `{{ word \| capitalize }}` |
| `striptags` | Remove HTML tags | `{{ html \| striptags }}` |
| `safe` | Mark as safe (no auto-escaping) | `{{ raw_html \| safe }}` |
| `escapejs` | Escape for JavaScript strings | `{{ val \| escapejs }}` |
| `floatformat` | Format float precision | `{{ price \| floatformat:2 }}` |
| `wordcount` | Count words in string | `{{ text \| wordcount }}` |
| `yesno` | Map true/false/nil to strings | `{{ flag \| yesno:"yes,no,maybe" }}` |
| `slice` | Slice a list | `{{ items \| slice:"1:3" }}` |
| `reverse` | Reverse a list or string | `{{ items \| reverse }}` |
| `pluralize` | Pluralize suffix based on count | `{{ count \| pluralize:"item,items" }}` |
| `center` | Center-pad a string | `{{ name \| center:20 }}` |
| `ljust` | Left-justify (right-pad) string | `{{ name \| ljust:20 }}` |
| `rjust` | Right-justify (left-pad) string | `{{ num \| rjust:10 }}` |
| `cut` | Remove all occurrences of a string | `{{ phone \| cut:"-" }}` |

## Control Structures

Pongo2 supports Jinja2-style control flow. These are available in any template context, though they are rarely needed because markov provides `for_each` and `when` at the step level.

```yaml
# Conditional
command: >
  {% if environment == "prod" %}
    deploy --replicas=3
  {% elif environment == "staging" %}
    deploy --replicas=1
  {% else %}
    echo "skipping deploy"
  {% endif %}

# Loop (prefer markov's for_each for step-level iteration)
script: >
  {% for host in servers %}
    echo "Checking {{ host }}"
  {% endfor %}

# Comments (stripped from output)
value: "{{ name }} {# this comment is removed #}"
```

## Where Templates Are Evaluated

Not every field in a step definition goes through template rendering. The following table shows which fields are template-evaluated.

| Field | Evaluated | Notes |
|---|---|---|
| `params` values | Yes | All string values in the params map |
| `vars` values | Yes | Both step-level and workflow-level |
| `when` | Yes | Evaluated as boolean expression |
| `artifacts[].path` | Yes | Artifact path field |
| `facts` values | Yes | Gate step fact values |
| `msg` | Yes | Assert step failure message |
| `that` expressions | Yes | Assert step conditions |
| `name` | No | Step name is a static identifier |
| `type` | No | Step type is resolved, not rendered |
| `register` | No | Register key is a literal context key |
| `for_each_key` | No | Field name, not a template |
| `for_each_sort` | No | Field name, not a template |
| `as` | No | Iterator variable name is literal |

## Boolean Evaluation

The `EvalBool` function (in `template.go:57`) evaluates a pongo2 expression and returns a boolean result. It works by wrapping the expression:

```
{% if <expr> %}true{% endif %}
```

If the rendered output (trimmed) equals `"true"`, the result is `true`. Otherwise, `false`.

**Used for:** `when` conditions, `assert` `that` expressions, and plain string values in `set_fact` vars (strings without `{{` or `{%` delimiters).

**Pongo2 truthiness rules:**

| Value | Truthy? |
|---|---|
| `nil` | false |
| `""` (empty string) | false |
| `0` (zero) | false |
| `false` | false |
| Empty list `[]` | false |
| Empty map `{}` | false |
| Everything else | true |

## Type Coercion

After template rendering in `set_fact` steps, string results are coerced to native Go types by `coerceString` (in `facts.go:146`). This ensures that computed values have the expected types downstream.

| Rendered String | Coerced Type | Coerced Value |
|---|---|---|
| `"true"` or `"True"` | `bool` | `true` |
| `"false"` or `"False"` | `bool` | `false` |
| `"None"` | `string` | `"None"` (kept as-is) |
| `""` (empty) | `string` | `""` (kept as-is) |
| `"42"` | `int` | `42` |
| `"3.14"` | `float64` | `3.14` |
| `'["a","b"]'` | `[]any` | Parsed JSON array |
| `'{"k":"v"}'` | `map[string]any` | Parsed JSON object |
| Anything else | `string` | Kept as-is |

Coercion order matters: integer parsing is attempted before float parsing. JSON parsing is only attempted if the string starts with `[` or `{`.

## Jev support

Step `when` and `assert.that` support durable `jev.noul`, `jev.choice`, and `jev.score` calls against named definitions. These helpers are not general template filters. See [Jev decisions](jev.md).
{% endraw %}

## Markov's pongo2

Markov builds with a patched copy of pongo2 v6.0.0 in `third_party/pongo2` (see its
`MARKOV_PATCHES.md`). Upstream stops parsing a variable after a `[subscript]`, so
`tiers[tier].tests` failed; the patch lets subscripts chain like dotted fields. pongo2 follows
Django's template language, not Jinja2's: there is no inline `a if cond else b` (use
`a | default:b` for "a, or b when a is empty", or `{% if %}...{% endif %}` in a string), and
`a or b` returns a boolean, not the first truthy value.
