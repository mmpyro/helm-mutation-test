# Mutators

The tool has ten mutators. Each one makes one kind of small change at one site. Each mutator
reports its change as a byte-range edit against the *original* file bytes. Nothing is rewritten in
place. This keeps the formatting exact, including Helm's whitespace-sensitive `{{-` trim markers. It
also lets a worker apply and revert one edit cheaply.

Use `--mutators` to select mutators and `--exclude-mutators` to remove them. See
[cli-reference.md](cli-reference.md#--mutators---exclude-mutators).

## At a glance

The counts below come from the fixture chart `testdata/charts/sample`, which has 472 mutants in
total. The tool scored it twice: once against its deliberately weak 4-test suite, and once against
its thorough 43-test suite.

| ID | mutates | mutants | invalid | weak k/s | strong k/s |
|---|---|---:|---:|---:|---:|
| [`bool-flip`](#bool-flip) | boolean literals | 15 | 0 | 0/14 | 15/0 |
| [`comparison-swap`](#comparison-swap) | comparison and boolean operators | 13 | 0 | 0/3 | 13/0 |
| [`cond-negate`](#cond-negate) | `if` / `else if` / `with` conditions | 19 | 0 | 0/10 | 19/0 |
| [`default-drop`](#default-drop) | `\| default X` pipeline segments | 7 | 0 | 0/4 | 7/0 |
| [`num-literal`](#num-literal) | numeric literals | 87 | 5 | 0/55 | 68/14 |
| [`pipe-drop`](#pipe-drop) | `quote`, `toYaml`, `trunc` and similar pipeline functions | 14 | 0 | 0/4 | 9/5 |
| [`range-empty`](#range-empty) | `range` loop expressions | 0 | 0 | — | — |
| [`required-drop`](#required-drop) | `required` guards | 3 | 0 | 0/2 | 3/0 |
| [`str-literal`](#str-literal) | string literals | 83 | 0 | 2/52 | 83/0 |
| [`yaml-key-delete`](#yaml-key-delete) | a key and its nested block | 231 | 15 | 4/135 | 209/7 |

`range-empty` shows zero because `testdata/charts/sample` contains no `range` at all. The tool
measures it against [`testdata/charts/rangeloop`](#range-empty) instead. It is as important to name a
mutator that generated nothing as a status that scored nothing. A blank row would read as "ran and
found no weakness".

`k/s` means killed/survived. A mutant counts as "survived" when every covering test still passed.
This is the raw outcome, before the tool's automatic equivalence check runs. The weak suite's zeros
are the main result: **seven of the mutators that fire on this chart score 0.0% against it**. A suite
of `isKind` and `exists` assertions cannot notice *anything* about content. The weak-column totals
are smaller than the mutant totals because the weak suite declares only two of the chart's seven
templates. The mutants in the other templates are `NoCoverage`.

Against the strong suite, only three mutators leave any survived mutants. **All 26 of those are
[equivalent mutants](concepts.md#equivalent-mutants)**, which no test can kill by construction. The
sections on [`num-literal`](#num-literal), [`pipe-drop`](#pipe-drop) and
[`yaml-key-delete`](#yaml-key-delete) describe the patterns. The tool detects these mutants
automatically and reports them as `Equivalent`, not `Survived`. A real run of `helm-mutation-test`
against the strong suite shows `0 survived` for all three mutators. It counts the 14, 5 and 7 above
under `Equivalent`. Against mutants that *can* be killed, the strong suite scores 100%. That 100% is
a measured number, because the denominator no longer holds any mutant that no suite could kill.

This table shows which assertion types killed the mutants in the strong run. The counts come from
its JSON report:

| mutator | killed by |
|---|---|
| `bool-flip` | `equal` ×10, `hasDocuments` ×2, `notExists` ×2, `lengthEqual` ×1 |
| `comparison-swap` | `equal` ×7, `notExists` ×2, `lengthEqual` ×2, `hasDocuments` ×2 |
| `cond-negate` | `equal` ×9, `lengthEqual` ×5, `hasDocuments` ×4, `notExists` ×1 |
| `default-drop` | `equal` ×7 |
| `num-literal` | `equal` ×65, `hasDocuments` ×2, `lengthEqual` ×1 |
| `pipe-drop` | `equal` ×9 |
| `range-empty` | none (does not fire on this chart) |
| `required-drop` | `failedTemplate` ×3 |
| `str-literal` | `equal` ×63, `isAPIVersion` ×7, `isKind` ×7, `failedTemplate` ×3, `lengthEqual` ×2, `notExists` ×1 |
| `yaml-key-delete` | `equal` ×175, `lengthEqual` ×19, `isAPIVersion` ×7, `isKind` ×7, `hasDocuments` ×1 |

`equal` does most of the work, but the other assertions matter too. `hasDocuments`, `notExists` and
`lengthEqual` kill a large share of the `cond-negate` and `bool-flip` mutants. These are the
assertions that say "this should *not* be here". Only `failedTemplate` kills `required-drop` mutants.

If your suites use mostly `exists` and `isKind`, your score will be low. That is the correct result.

## Where mutators apply

| mutator | templates (AST, inside `{{ }}`) | templates (plain YAML lines) | `values.yaml` |
|---|:-:|:-:|:-:|
| `bool-flip` | ✔ | ✔ | ✔ |
| `comparison-swap` | ✔ | | |
| `cond-negate` | ✔ | | |
| `default-drop` | ✔ | | |
| `num-literal` | ✔ | ✔ | ✔ |
| `pipe-drop` | ✔ | | |
| `range-empty` | ✔ | | |
| `required-drop` | ✔ | | |
| `str-literal` | ✔ | ✔ | ✔ |
| `yaml-key-delete` | | ✔ (line-based) | ✔ (node tree) |

"Plain YAML lines" means hardcoded values on template lines that contain no action, for example a
literal `hostNetwork: false` or `metrics-path: /metrics`. These values have no AST node. But a suite
that never asserts them has exactly the weakness the tool looks for.

---

## `bool-flip`

**Flip boolean literals (`true` ↔ `false`).**

The mutator keeps the casing and quoting, so `"True"` becomes `"False"`, not `false`:

| before | after |
|---|---|
| `true` | `false` |
| `TRUE` | `FALSE` |
| `True` | `False` |
| `"true"` | `"false"` |
| `'false'` | `'true'` |

It applies to bool literals inside actions, to hardcoded YAML booleans on action-free template lines
and to boolean scalars in `values.yaml`. It never flips mapping *keys*. Renaming a key literally
named `true` would turn it into a different field.

**Before / after.** A hardcoded container security flag, `templates/deployment.yaml:37`:

```diff
           securityContext:
             allowPrivilegeEscalation: false
-            readOnlyRootFilesystem: true
+            readOnlyRootFilesystem: false
             privileged: false
```

A default in `values.yaml:52`:

```diff
 podDisruptionBudget:
-  enabled: true
+  enabled: false
   minAvailable: 1
```

A bool literal inside an action, `templates/deployment.yaml:30`:

```diff
-        runAsNonRoot: {{ .Values.securityContext.runAsNonRoot | default true }}
+        runAsNonRoot: {{ .Values.securityContext.runAsNonRoot | default false }}
```

**Why it matters.** Flipping a `values.yaml` default is the cheapest way to find out whether any test
pins that default. Take a chart that ships `serviceAccount.create: true`. If its suite never asserts
that the ServiceAccount exists, the suite will not notice the flip.

**The weak test it exposes.** A suite that never tests a feature toggle's *shipped* value. All 14
scored `bool-flip` mutants survive the weak suite. The strong suite kills all 15. It kills 10 with
`equal`, but the rest need assertions that check absence:

- `hasDocuments` catches `podDisruptionBudget.enabled: true → false`.
- `notExists` catches both `ingress.tls.enabled` and `config.debug`.

Without those assertions, a toggle flipped *off* produces a smaller manifest that an `equal`-only
suite never looks at.

---

## `comparison-swap`

**Swap a comparison or boolean operator for its opposite.**

| from | to |
|---|---|
| `eq` | `ne` |
| `ne` | `eq` |
| `lt` | `ge` |
| `ge` | `lt` |
| `gt` | `le` |
| `le` | `gt` |
| `and` | `or` |
| `or` | `and` |

The mutator swaps only the *head* of a command. An operator name in a later argument position is a
value, not a call. `{{ .Values.eq }}` is a field named `eq`. The mutator leaves `not` alone, because
negating a negation is the job of [`cond-negate`](#cond-negate).

**Before / after.** A numeric threshold in `templates/configmap.yaml:16`:

```diff
-  cache-enabled: {{ ge (int .Values.config.cacheSizeMb) 64 | quote }}
+  cache-enabled: {{ lt (int .Values.config.cacheSizeMb) 64 | quote }}
```

A compound guard in `templates/poddisruptionbudget.yaml:1` yields two candidates, one for `and` and
one for `gt`:

```diff
- {{- if and .Values.podDisruptionBudget.enabled (gt (int .Values.replicaCount) 1) }}
+ {{- if or  .Values.podDisruptionBudget.enabled (gt (int .Values.replicaCount) 1) }}
```

```diff
- {{- if and .Values.podDisruptionBudget.enabled (gt (int .Values.replicaCount) 1) }}
+ {{- if and .Values.podDisruptionBudget.enabled (le (int .Values.replicaCount) 1) }}
```

The mutator replaces only the operator. The operands do not change.

**Why it matters.** `eq`/`ne` and the ordering pairs are true inversions. `and`/`or` is not an
inversion, but it changes *which* inputs produce output. That exposes a suite that tests only one
combination of flags. This is the common case for a chart with several interacting toggles.

**The weak test it exposes.** A suite that tests one side of a branch and not the other. The weak
suite kills none of its 3 scored swaps. The strong suite kills all 13, and again the mix of
assertions is important:

- `hasDocuments` catches both swaps in the PodDisruptionBudget guard. Either swap makes the resource
  appear or vanish.
- `notExists` catches the `and → or` in `templates/configmap.yaml:26` and `templates/ingress.yaml:14`.
- `lengthEqual` catches the two `eq` swaps in the deployment's `env` branch.

Plain `equal` catches the three `ternary`-driven swaps in `configmap.yaml` (`ge`, `lt`, `le` at lines
16, 18 and 20). Each one flips a rendered ConfigMap value directly.

---

## `cond-negate`

**Negate `if`, `else if` and `with` conditions: `COND` → `not (COND)`.**

The mutator puts the condition in parentheses because it can be a multi-argument call, and
`not and .a .b` would not parse. It does not negate `range`, because there is no boolean to invert.
Each `else if` yields its own candidate, so the mutator covers the full chain.

**Before / after.** A whole-resource guard, `templates/hpa.yaml:1`:

```diff
- {{- if .Values.autoscaling.enabled }}
+ {{- if not (.Values.autoscaling.enabled) }}
```

A compound condition in `templates/_helpers.tpl:21`, in parentheses so it still parses:

```diff
- {{- if and .Values.serviceAccount.create .Values.serviceAccount.name -}}
+ {{- if not (and .Values.serviceAccount.create .Values.serviceAccount.name) -}}
```

The mutator negates an already-negated condition again and does not simplify it,
`templates/deployment.yaml:8`:

```diff
-   {{- if not .Values.autoscaling.enabled }}
+   {{- if not (not .Values.autoscaling.enabled) }}
```

A `with` block, `templates/deployment.yaml:17`:

```diff
-       {{- with .Values.podAnnotations }}
+       {{- with not (.Values.podAnnotations) }}
```

Trim markers survive exactly. The mutator also finds conditions inside `define` blocks.
`_helpers.tpl` is mostly define blocks, and if the mutator missed them, it would miss all of a
chart's helper logic. The fixture chart's `else if` chain at `templates/deployment.yaml:79` and `:82`
produces one candidate for each line.

**Why it matters.** This is the highest-value mutator for Helm charts. Charts guard whole resources
behind `{{- if .Values.ingress.enabled }}`. A suite that tests only the enabled case never notices
when the resource appears and should not.

**The weak test it exposes.** A missing negative case. The weak suite kills none of its 10 scored
negations. The strong suite kills all 19, and **10 of those 19 need an assertion other than
`equal`**:

- `hasDocuments` ×4, for the four whole-resource guards (`hpa.yaml:1`, `ingress.yaml:1`,
  `serviceaccount.yaml:1`, `poddisruptionbudget.yaml:1`).
- `lengthEqual` ×5, for the conditional list entries.
- `notExists` ×1, for the ingress TLS block.

That ratio is the practical lesson. For `cond-negate`, you cannot assert "this resource should not
exist" without `hasDocuments`, `notExists` or `lengthEqual`. More `equal` assertions do not help, no
matter how many you add.

---

## `default-drop`

**Remove a `| default X` segment from a pipeline.**

The mutator keeps the rest of the pipeline. This includes a `default` in the middle of a longer
chain:

| before | after |
|---|---|
| `{{ .Values.image.tag \| default .Chart.AppVersion }}` | `{{ .Values.image.tag }}` |
| `{{ .Values.replicaCount \| default 3 }}` | `{{ .Values.replicaCount }}` |
| `{{ .Values.tag \| default "latest" \| quote }}` | `{{ .Values.tag \| quote }}` |

The mutator does not change:

- Pipelines without a `default` segment.
- A *field* named `default` (`{{ .Values.default }}`).
- `default` in leading position (`{{ default .Values.a }}`), where there is nothing to pipe from.

**Before / after.** All seven `default-drop` mutants from the fixture chart. Together they show every
shape:

| site | mutation |
|---|---|
| `_helpers.tpl:2` | `{{- $name := .Values.nameOverride \| default .Chart.Name -}}` → `{{- $name := .Values.nameOverride -}}` |
| `configmap.yaml:8` | `{{ .Values.config.logLevel \| default "info" \| quote }}` → `{{ .Values.config.logLevel \| quote }}` |
| `configmap.yaml:13` | `{{ .Values.config.traceSampleRatio \| default 0.1 \| quote }}` → `{{ .Values.config.traceSampleRatio \| quote }}` |
| `deployment.yaml:25` | `{{ .Values.terminationGracePeriodSeconds \| default 30 }}` → `{{ .Values.terminationGracePeriodSeconds }}` |
| `deployment.yaml:30` | `{{ .Values.securityContext.runAsNonRoot \| default true }}` → `{{ .Values.securityContext.runAsNonRoot }}` |
| `deployment.yaml:33` | `{{ .Values.image.tag \| default .Chart.AppVersion }}` → `{{ .Values.image.tag }}` |
| `serviceaccount.yaml:8` | `{{ .Values.serviceAccount.automount \| default true }}` → `{{ .Values.serviceAccount.automount }}` |

Look at the two middle-of-pipeline cases. When the mutator drops `| default "info"`, `| quote` stays.

**Why it matters.** `{{ .Values.image.tag | default .Chart.AppVersion }}` has two behaviours: the
value path and the fallback path. By far most suites test only the value path. The fallback decides
what a *fresh install* renders, and those suites leave it entirely unasserted. When the mutator drops
the default, the field renders empty.

**The weak test it exposes.** A suite that only overrides the value and never tests the fallback. The
fixture chart makes this concrete. Its `values.yaml` *deliberately leaves `image.tag`,
`terminationGracePeriodSeconds`, `serviceAccount.automount`, `securityContext.runAsNonRoot` and
`config.logLevel` undeclared or empty*, with comments that say so. This makes the `| default` branch
the branch that actually renders.

The weak suite kills none of its 4 scored drops. The strong suite kills all 7, each one with `equal`.
This is the one mutator where plain value assertions are enough, because dropping a default always
changes a rendered value.

---

## `num-literal`

**Perturb numeric literals, two variants per site: `n` → `n+1`, and `n` → `0`.**

Each variant has its own mutant ID (`…:num-literal:increment`, `…:num-literal:zero`). The mutator
removes duplicates, so no variant is a no-op. For `0`, the zero variant would change nothing, so the
mutator keeps the increment (`1`) and drops the duplicate.

The mutator keeps the quoting, the integer or float form and the original number of decimal places:

| original | increment | zero |
|---|---|---|
| `3` | `4` | `0` |
| `-2` | `-1` | `0` |
| `0` | `1` | *(dropped as a duplicate)* |
| `1.5` | `2.5` | `0.0` |
| `0.25` | `1.25` | `0.00` |

It applies to numbers inside actions, to hardcoded YAML numbers on action-free template lines and to
numeric scalars in `values.yaml`.

**Before / after.** A hardcoded field, `templates/deployment.yaml:41`:

```diff
             - name: http
-              containerPort: 80
+              containerPort: 81
```

An argument to an arithmetic function, `templates/deployment.yaml:63`:

```diff
-            initialDelaySeconds: {{ mul .Values.probes.initialDelaySeconds 2 }}
+            initialDelaySeconds: {{ mul .Values.probes.initialDelaySeconds 3 }}
```

A float in `values.yaml:82`. The decimal places stay the same:

```diff
-  backoffFactor: 1.5
+  backoffFactor: 2.5
```

**Layout arguments are deliberately not exempt.** The mutator changes `nindent 4`, `indent 8`,
`trunc 63` and `substr 0 5`. An earlier version had a guard here. It assumed that reindenting always
breaks rendering and gives a useless `Invalid` mutant. Measurement disproved the "always".
`TestNumLiteralMutatesLayoutArguments` pins this behaviour.

The fixture chart shows the result at all 14 of its `nindent`/`toYaml | nindent` sites:

- **Every `+1` variant survives.** That is all 14, and **all 14 are
  [equivalent mutants](concepts.md#equivalent-mutants)**. YAML ignores the absolute indentation of a
  block mapping. It only requires the block to be deeper than its parent. So `nindent 4 → 5` emits
  different bytes but identical parsed data. No assertion can catch these mutants, and chasing them
  wastes effort.
- **Every `→ 0` variant is either killed or breaks rendering:** 9 `Killed`, 5 `Invalid`. An
  indentation of zero merges the block into its parent. This either changes a value that a test
  pins, or produces YAML that no longer parses (`yaml: line 22: did not find expected key`).

So on an `indent`/`nindent` argument, the increment variant is not a useful mutant, and the `→ 0`
variant at the same site is. A check rendered both variants and compared the parsed documents. The
tool's status matched that independent comparison at all 14 sites.

`num-literal` is one of only two mutators that leave anything unkilled in the strong run. All 14 of
those mutants are that unkillable increment. The tool's automatic check reports them as
`Equivalent`, not `Survived`. Where the `→ 0` mutation does break rendering, the `Invalid`
classification reports it openly, not silently.

**Why it matters.** The increment variant catches off-by-one blindness. The zero variant catches
fields that no test asserts at all.

**The weak test it exposes.** `exists: spec.ports` cannot tell port 80 from port 81. An assertion
that checks presence and not value cannot see any mutant from this mutator. That is why the weak
suite scores 0.0% on all 55 of its scored `num-literal` mutants. The strong suite kills 68 of 82, 65
of them with plain `equal`.

---

## `pipe-drop`

**Remove a type- or format-changing function from a pipeline, keeping the value it was applied to.**

The mutator changes both call forms. It removes a piped segment whole. From a prefix call, it
removes the function and any leading arguments.

| before | after |
|---|---|
| `{{ .Values.port \| quote }}` | `{{ .Values.port }}` |
| `{{ .Release.Name \| trunc 63 \| trimSuffix "-" }}` | `{{ .Release.Name \| trimSuffix "-" }}`, and separately `{{ .Release.Name \| trunc 63 }}` |
| `{{ quote .Values.deep.x }}` | `{{ .Values.deep.x }}` |
| `{{ toYaml .Values.annotations }}` | `{{ .Values.annotations }}` |
| `{{ eq (.Values.mode \| lower) "fast" }}` | `{{ eq (.Values.mode) "fast" }}` |

It drops these functions: `quote`, `squote`, `toString`, `toYaml`, `toJson`, `toPrettyJson`, `trim`,
`lower`, `upper`, `title` and `b64enc`. It also drops `trunc`, `trimPrefix` and `trimSuffix`,
together with their leading argument.

The mutator does not change:

- `default` and `required`, which have mutators of their own.
- `include`, `tpl` and `printf`, which produce the value and do not transform it.
- `indent`/`nindent`, whose widths `num-literal` already perturbs.
- A call whose argument count does not match the known shape. `quote` is variadic, and dropping it
  from `quote .Values.a .Values.b` would keep only one of the two values.

**Two exclusions, both measured rather than assumed.** Before these exclusions existed, all seven
such mutants in the fixture chart rendered `Invalid`:

| left out | why | fixture evidence |
|---|---|---|
| `int`, `int64`, `float64` | Helm decodes every values number as float64, so the conversions exist to feed a comparison against an integer literal | 4 of 4 sites: `incompatible types for comparison: float64 and int` |
| `toYaml`, `toJson`, `toPrettyJson` feeding `indent`/`nindent` | an indenter accepts only a string, and without the serialiser it gets the map itself | 3 of 3 sites: `wrong type for value; expected string; got map[string]interface {}` |

The mutator still drops a serialiser that is *not* followed by an indenter, as in the table above.

**Before / after.** A numeric config value, `templates/configmap.yaml:10`:

```diff
-  max-connections: {{ .Values.config.maxConnections | quote }}
+  max-connections: {{ .Values.config.maxConnections }}
```

ConfigMap `data` values must be strings, and `env[].value` must be a string too. The unquoted form
renders the integer `256`, and the API server rejects it.

**Why it matters.** These functions decide the *type* or *shape* of what renders, as well as its
content:

- Dropping `quote` turns the string `"8080"` into the integer `8080`.
- Dropping `trunc 63` lets a generated name exceed the 63-character DNS label limit.
- Dropping `trimSuffix "-"` leaves a trailing dash that Kubernetes refuses.

A suite that asserts presence, or compares a value without its type, never notices.

**The weak test it exposes.** An `exists` on a field where an `equal` on its exact value was needed,
and no test with a release name long enough to reach `trunc`. The fixture yields 14 `pipe-drop`
mutants: 12 `quote`, one `trunc 63` and one `trimSuffix "-"`. The weak suite kills none of its 4
scored drops.

The strong suite kills 9, all with `equal`. Seven are `| quote` drops on numbers and
booleans that the suite pins as strings (`"256"`, `"true"`). The other two are both `_helpers.tpl:3`
segments. A test that renders a release name long enough to be truncated catches them.

**The 5 left unkilled in the strong run are all `| quote` on a value that is already a plain
string.** These values are `.Chart.AppVersion`, `config.logLevel`, `config.logFormat`, the `ternary`
result at `configmap.yaml:18` and `apiToken`. The tool reports them as `Equivalent`. YAML parses
`info` and `"info"` to the same string, so no assertion can tell the two renders apart. Against the
weak suite, the two `_helpers.tpl:3` segments are equivalent too. None of its four tests renders a
name long enough for `trunc` to change anything.

---

## `range-empty`

**Force a `range` loop to iterate zero times: `range X` → `range list`.**

`list` is sprig's empty-list constructor. With it, the loop body never runs, and everything the body
produced disappears from the manifest. The mutator keeps variable declarations: `range $k, $v := X`
becomes `range $k, $v := list`. If it dropped them, the body's `$k` and `$v` would be undeclared and
the template would no longer parse. An unparseable mutant is `Invalid`. Every test catches it,
whatever the test asserts, so it grades nothing.

**Before / after.** A list, `testdata/charts/rangeloop/templates/configmap.yaml:7`:

```diff
-   {{- range .Values.ports }}
+   {{- range list }}
```

A map, where the declarations survive, `:10`:

```diff
-   {{- range $k, $v := .Values.labels }}
+   {{- range $k, $v := list }}
```

The mutator replaces the whole expression, not only its first term. So a piped source collapses too:

```diff
- {{- range .Values.hosts | sortAlpha }}
+ {{- range list }}
```

If the `range` has an `{{ else }}`, the mutant renders the **else** branch, not nothing. That is
still a change to the manifest, so it is still a useful mutant. But for that shape, "the block
disappears" is the wrong intuition.

**Why it matters.** Before this mutator, no mutator changed loops. `cond-negate` covers `if` and
`with`, and nothing targeted `range`. Charts have many loops: env vars, ports, ingress hosts, volume
mounts, image pull secrets. So a suite could assert nothing at all about the output of any loop and
still score 100%. `range-empty` does for `range` what `cond-negate` does for `if`.

**The weak test it exposes.** A suite that checks that a resource exists, but never checks what its
loops produced. `isKind` and a single `equal` on a static key cannot notice when every port, label or
host vanishes at once. The measurement uses `testdata/charts/rangeloop`, whose four loops yield four
mutants. The weak suite kills **0 of 2** scored mutants. The strong suite kills **2 of 2**. The other
two are `Equivalent` under both suites (see below).

**When it is correctly excluded.** Take a loop over a collection that is empty under every covering
test context. Its mutant cannot change what renders, so no assertion could catch it either. The tool
reports such mutants as [`Equivalent`](concepts.md#equivalent-mutants). Like any other unkillable
mutant, they leave the score's denominator.

`range` evaluates its pipeline even when the result is
empty. So the execution probe can prove that the loop was reached, and the verdict rests on evidence,
not on a guess. The fixture's `extras: []` and `extraLabels: {}` loops are exactly this case. The tool
reports both as `Equivalent` under both suites. `extraLabels` also proves that the probe still parses
when the loop it replaces carries a `$k, $v :=` declaration.

---

## `required-drop`

**Strip a `required "msg" X` guard down to plain `X`.**

The deletion removes `required "msg" ` and leaves the value argument in place. The surrounding
pipeline survives:

| before | after |
|---|---|
| `{{ required "token is required" .Values.token }}` | `{{ .Values.token }}` |
| `{{ required "msg" .Values.token \| quote }}` | `{{ .Values.token \| quote }}` |

A `required` call with no value argument has nothing to keep, so the mutator leaves it alone.

**Before / after.** All three of the fixture chart's `required` guards:

```diff
  # templates/deployment.yaml:74
-               value: {{ required "apiToken is required" .Values.apiToken | quote }}
+               value: {{ .Values.apiToken | quote }}
```

```diff
  # templates/deployment.yaml:76
-               value: {{ required "adminEmail is required" .Values.adminEmail }}
+               value: {{ .Values.adminEmail }}
```

```diff
  # templates/ingress.yaml:21
-     - host: {{ required "ingress.host is required when the ingress is enabled" .Values.ingress.host }}
+     - host: {{ .Values.ingress.host }}
```

The first case shows why the mutator keeps the pipeline: `| quote` stays.

The mutator anchors the deletion on the *end of the message argument*, not on the position of the
value. A `FieldNode` reports a position past its first segment. If the anchor were `.Values.token`,
the field would appear to start at `.token`, and the deletion would also remove `.Values`. See
[the gotchas in `CLAUDE.md`](https://github.com/mmpyro/helm-mutation-test/blob/main/CLAUDE.md#gotchas-that-have-already-cost-time).

**Why it matters.** `required` is the chart author's contract that the user must supply a value. The
only way to verify it is a negative test: helm-unittest's `failedTemplate` assertion. Negative tests
are exactly what suites tend to lack. If removing the guard changes nothing, no test checks the
failure path.

**The weak test it exposes.** A missing `failedTemplate` assertion. This is the one mutator in the set
that **only** a `failedTemplate` can kill. `failedTemplate` kills all three of the fixture chart's
`required-drop` mutants, and nothing else kills them. The weak suite kills neither of its 2 scored
mutants.

If your chart uses `required` and this mutant survives, you have an unverified contract.

[`str-literal`](#str-literal) also mutates the `required` *message*. That is a separate mutant, and it
is useful too. `failedTemplate`'s `errorMessage` asserts the message. A suite that checks the message
kills the mutant, and a suite that does not check it lets it survive. Three of the fixture's
`str-literal` kills come from exactly this case.

---

## `str-literal`

**Replace a string literal with the sentinel `helm-mutation-test`.**

The sentinel is easy to recognise on purpose. If it leaks into a rendered manifest during debugging,
its origin is obvious. The mutator keeps the quote style. For an unquoted literal it uses double
quotes, so the sentinel is always an unambiguous YAML string.

It applies to string literals inside actions, to hardcoded string values on action-free template
lines and to string scalars in `values.yaml`. It skips a literal that already equals the sentinel.

**Before / after.** A hardcoded field with no action at all, `templates/deployment.yaml:42`:

```diff
-              protocol: TCP
+              protocol: "helm-mutation-test"
```

A string operand inside a condition, `templates/deployment.yaml:79`:

```diff
-             {{- if eq .Values.env "prod" }}
+             {{- if eq .Values.env "helm-mutation-test" }}
```

A `ternary` branch, `templates/service.yaml:9`:

```diff
-   externalTrafficPolicy: {{ ternary "Local" "Cluster" .Values.service.external }}
+   externalTrafficPolicy: {{ ternary "helm-mutation-test" "Cluster" .Values.service.external }}
```

A `values.yaml` scalar, `values.yaml:7`:

```diff
 image:
   repository: nginx
-  pullPolicy: IfNotPresent
+  pullPolicy: "helm-mutation-test"
```

**Two exclusions, both narrow:**

- **`include` / `template` / `tpl` target strings.** A replaced target makes the call look up a
  template that does not exist, and that is a render error. On the fixture chart, every suite kills
  this mutant alike, so it grades nothing and only adds `Invalid` noise. This is the only exclusion
  in the whole mutator set that evidence supports.
- **Format strings** (anything containing a `%` verb). The sentinel has no verbs, so the arguments
  stay unused, and Go writes `%!(EXTRA …)` into the manifest. That is a confusing artifact, not a
  clean mutation. This exclusion is a scoping choice, not a rendering problem.

The mutator explicitly does **not** exclude a `required` message. That mutant survives a weak suite,
and a suite that asserts the message through `failedTemplate` catches it. That is a real finding.

**Why it matters.** This is the mutator that punishes presence-only assertions. A suite that asserts
that `imagePullPolicy` *exists*, but not what it *equals*, cannot tell `IfNotPresent` from a
sentinel.

**The weak test it exposes.** `exists:` where `equal:` was needed. The weak suite kills only **2 of
54** scored `str-literal` mutants, and both kills are accidents. Those two mutants hit `kind:`
(`kind: Deployment` and `kind: Service`), and the suite's `isKind` assertions happen to cover them.
Every mutant with actual content in it survives.

The strong suite kills **all 83**, with the widest spread of assertions of any mutator: `equal` ×63,
`isAPIVersion` ×7, `isKind` ×7, `failedTemplate` ×3 (the `required` messages), `lengthEqual` ×2 and
`notExists` ×1. This mutator rewards explicit `apiVersion` and `kind` assertions more than any other
mutator does.

---

## `yaml-key-delete`

**Delete a key together with its nested block.**

This is the bluntest mutator and often the most revealing one. It is also the largest by volume: 231
of the fixture chart's 472 mutants.

For `values.yaml`, it uses the YAML node tree, which knows the real structure. For templates, it
works line by line. A template is not valid YAML before rendering, so it has no node tree.

**Before / after.** A single line, `templates/deployment.yaml:34`:

```diff
-           imagePullPolicy: {{ .Values.image.pullPolicy }}
+ (line removed)
```

A key and its whole nested block, `templates/deployment.yaml:35`:

```diff
-           securityContext:
-             allowPrivilegeEscalation: false
-             readOnlyRootFilesystem: true
-             privileged: false
+ (4 lines removed)
```

Reports show only the first deleted line and a count, because a block can be long. In the fixture,
deleting the `ingress:` key in `values.yaml` removes 10 lines, and the `spec:` deletions in
`templates/deployment.yaml` remove dozens.

**Two safety rules, both about template syntax rather than semantics:**

- The mutator skips a key line that contains an action only when the action is *control flow*
  (`if`/`else`/`end`/`range`/`with`/`define`/`block`). Deleting half of an `if`/`end` pair leaves an
  unbalanced template. That is a syntax error, not a mutation.
- For the same reason, it skips a key whose block contains unbalanced control flow. A block is
  eligible when its openers and `{{ end }}`s balance and it contains no `{{ else }}`.

**No key is exempt from deletion.** Earlier versions skipped `apiVersion`, `kind` and `metadata`. The
assumption was that Helm rejects a manifest without them. Measurement disproved that.

All three
render fine when removed. They survive the weak suite, and the strong suite catches them with
`isAPIVersion` and `isKind`, ×7 each. That is exactly what those assertions are for. Even the *weak*
suite catches a deleted `kind`. That is the tool working as intended, not noise.

**Why it matters.** If a whole field can vanish from a rendered manifest and every test still passes,
no test asserts that field at all.

**The weak test it exposes.** Everything that `exists` and `isKind` do not reach. The weak suite kills
**4 of 139** scored deletions. Those four kills match the reach of its four assertions exactly.
`isKind` catches two `kind:` lines, and `exists` catches the Service's `spec:` and `ports:` blocks.
Every deletion with content in it survives: `replicas`, `imagePullPolicy`, `containerPort`, both
probes, `resources`, the whole `env:` block, every label.

**The 7 mutants left unkilled in the strong run are all `values.yaml` keys.** The tool reports them
as `Equivalent`, not `Survived`. In each case, deleting the key changes nothing that renders:

| survivor | why deleting it renders identically |
|---|---|
| `values.yaml:6` `tag: ""` | the template's `\| default .Chart.AppVersion` produces the same output either way |
| `values.yaml:14` `name: ""` | empty and absent both fall through `sample.serviceAccountName` to the same branch |
| `values.yaml:44` `enabled: false` (autoscaling) | absent is falsey, so the HPA stays absent |
| `values.yaml:49` `targetMemoryUtilizationPercentage: 0` | `0` is falsey, so the memory metric was already omitted |
| `values.yaml:66` `enabled: false` (ingress) | absent is falsey |
| `values.yaml:73` `enabled: false` (ingress TLS) | absent is falsey |
| `values.yaml:83` `debug: false` | absent is falsey |

The pattern applies in general: **deleting a `values.yaml` key whose value is already its type's zero
value is almost always an [equivalent mutant](concepts.md#equivalent-mutants).** Without the key, the
value is nil. Go templates treat nil and the zero value the same way, both for truthiness and for
`| default`. All seven above are `""`, `false` or `0`.

These seven render **byte-identical** manifests, not only parsed-equal ones. This was verified under
the chart's default values and with every feature toggle enabled. No assertion can catch them, so the
tool's equivalence check reclassifies all seven. As a result, `yaml-key-delete` scores a genuine 100%
against the strong suite. A naive killed/survived count would show 96.8%.

The equivalence also holds under value overrides. A suite that sets the key supplies its own value,
whether or not `values.yaml` declares it. So the deletion is invisible under every value set.

**This mutator produces 15 of the strong run's 20 `Invalid` mutants**, all from `values.yaml`. When
the mutator deletes a whole values block, the template dereferences nil:

```
values.yaml:3   image:                 → nil pointer evaluating interface {}.repository
values.yaml:12  serviceAccount:        → nil pointer evaluating interface {}.create
values.yaml:22  securityContext:       → nil pointer evaluating interface {}.runAsUser
values.yaml:35  probes:                → nil pointer evaluating interface {}.enabled
values.yaml:76  config:                → nil pointer evaluating interface {}.logLevel
values.yaml:88  apiToken: …            → execution error: apiToken is required
values.yaml:89  adminEmail: …          → execution error: adminEmail is required
```

*(7 of 15 shown.)* The score correctly excludes these mutants, because every test "catches" them,
whatever it asserts. Twenty invalid mutants out of 472 is a rate of 4.2%. That is well under the 20%
ceiling that `TestInvalidMutantRateIsLow` enforces.

If this mutator dominates your report and you want a quicker signal, use `--exclude-mutators
yaml-key-delete`. The nine more surgical mutators remain.

---

## Adding a mutator, or a guard

A guard needs evidence, not intuition. Four guards were removed from this codebase because
measurement disproved their premise. Before you add a guard, verify that the mutation actually breaks
rendering:

```console
# copy the fixture, apply the mutation by hand, then:
helm unittest -f 'tests/weak_test.yaml'   /tmp/probe   # should survive
helm unittest -f 'tests/strong_test.yaml' /tmp/probe   # should be killed
```

If the mutant survives one suite and the other suite kills it, it is a useful mutant. Do not guard
it. A guard is justified only where the mutation errors whatever the values are, as with
`pipe-drop`'s [two exclusions](#pipe-drop).

"Sometimes breaks rendering" is not a reason for a guard
either. On the fixture chart, `num-literal` on `nindent` produces 14 survivors, 9 kills and 5
invalids. The useful signal in the first two groups outweighs the noise in the third. Watch the
invalid rate as you work. `TestInvalidMutantRateIsLow` fails above 20%.

Shared invariants live in loops over `IDs()` in `internal/mutator/mutator_test.go`. A new mutator
inherits them automatically:

- No no-op candidates.
- No inverted spans.
- No panics on unparseable templates.
- Mutants still parse.
