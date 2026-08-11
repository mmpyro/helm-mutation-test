# Mutators

Nine mutators, each producing one kind of small, single-site change. Every one reports a byte-range
edit against the *original* file bytes; nothing is ever rewritten in place. That keeps formatting
exact — including Helm's whitespace-sensitive `{{-` trim markers — and lets a worker apply and revert
one edit cheaply.

Select with `--mutators`, subtract with `--exclude-mutators`. See
[cli-reference.md](cli-reference.md#--mutators---exclude-mutators).

## At a glance

Counts below are from the fixture chart `testdata/charts/sample`, 458 mutants total, scored twice:
once against its deliberately weak 4-test suite and once against its thorough 43-test one.

| ID | mutates | mutants | invalid | weak k/s | strong k/s |
|---|---|---:|---:|---:|---:|
| [`bool-flip`](#bool-flip) | boolean literals | 15 | 0 | 0/14 | 15/0 |
| [`comparison-swap`](#comparison-swap) | comparison and boolean operators | 13 | 0 | 0/3 | 13/0 |
| [`cond-negate`](#cond-negate) | `if` / `else if` / `with` conditions | 19 | 0 | 0/10 | 19/0 |
| [`default-drop`](#default-drop) | `\| default X` pipeline segments | 7 | 0 | 0/4 | 7/0 |
| [`num-literal`](#num-literal) | numeric literals | 87 | 5 | 0/55 | 68/14 |
| [`range-empty`](#range-empty) | `range` loop expressions | 0 | 0 | — | — |
| [`required-drop`](#required-drop) | `required` guards | 3 | 0 | 0/2 | 3/0 |
| [`str-literal`](#str-literal) | string literals | 83 | 0 | 2/52 | 83/0 |
| [`yaml-key-delete`](#yaml-key-delete) | a key and its nested block | 231 | 15 | 4/135 | 209/7 |

`range-empty` shows zero because `testdata/charts/sample` contains no `range` at all. It is measured
against [`testdata/charts/rangeloop`](#range-empty) instead. A mutator that generated nothing is as
worth naming as a status that scored nothing — a blank row would read as "ran and found no weakness".

`k/s` is killed/survived, counting a mutant as "survived" whenever every covering test still passed —
the raw outcome before the tool's automatic equivalence check runs. The weak suite's zeros are the
headline: **six of the mutators that fire on this chart score 0.0% against it**, because a suite of `isKind` and `exists`
assertions cannot notice *anything* about content. Its weak-column totals are smaller than the mutant
totals because it declares only two of the chart's seven templates, so the rest are `NoCoverage`.

Only two mutators leave any such case against the strong suite, and **every one of those 21 is an
[equivalent mutant](concepts.md#equivalent-mutants)** — unkillable by construction, in the two patterns
noted under [`num-literal`](#num-literal) and [`yaml-key-delete`](#yaml-key-delete). The tool detects
this automatically and reports them as `Equivalent`, not `Survived`: a real run of `helm-mutation-test`
against the strong suite shows `0 survived` for both mutators, with the 14 and 7 above counted under
`Equivalent` instead. Against mutants that *can* be killed, the strong suite scores 100% — a real
number now, not an aspiration, since nothing is left in the denominator that no suite could ever kill.

Which assertion types actually did the killing in the strong run, measured from its JSON report:

| mutator | killed by |
|---|---|
| `bool-flip` | `equal` ×10, `hasDocuments` ×2, `notExists` ×2, `lengthEqual` ×1 |
| `comparison-swap` | `equal` ×7, `notExists` ×2, `lengthEqual` ×2, `hasDocuments` ×2 |
| `cond-negate` | `equal` ×9, `lengthEqual` ×5, `hasDocuments` ×4, `notExists` ×1 |
| `default-drop` | `equal` ×7 |
| `num-literal` | `equal` ×65, `hasDocuments` ×2, `lengthEqual` ×1 |
| `required-drop` | `failedTemplate` ×3 |
| `str-literal` | `equal` ×63, `isAPIVersion` ×7, `isKind` ×7, `failedTemplate` ×3, `lengthEqual` ×2, `notExists` ×1 |
| `yaml-key-delete` | `equal` ×175, `lengthEqual` ×19, `isAPIVersion` ×7, `isKind` ×7, `hasDocuments` ×1 |

`equal` does most of the work, but the spread matters: `cond-negate` and `bool-flip` are killed
substantially by `hasDocuments`, `notExists` and `lengthEqual` — the assertions that say "this should
*not* be here". `required-drop` is killed only ever by `failedTemplate`.

If your suites are mostly `exists` and `isKind`, your score will be low, and that is the correct
answer.

## Where mutators apply

| mutator | templates (AST, inside `{{ }}`) | templates (plain YAML lines) | `values.yaml` |
|---|:-:|:-:|:-:|
| `bool-flip` | ✔ | ✔ | ✔ |
| `comparison-swap` | ✔ | | |
| `cond-negate` | ✔ | | |
| `default-drop` | ✔ | | |
| `num-literal` | ✔ | ✔ | ✔ |
| `range-empty` | ✔ | | |
| `required-drop` | ✔ | | |
| `str-literal` | ✔ | ✔ | ✔ |
| `yaml-key-delete` | | ✔ (line-based) | ✔ (node tree) |

"Plain YAML lines" means hardcoded values on template lines that contain no action at all — a literal
`hostNetwork: false` or `metrics-path: /metrics`. Those have no AST node, but a suite that never
asserts them is exactly the weakness being hunted.

---

## `bool-flip`

**Flip boolean literals (`true` ↔ `false`).**

Casing and quoting are preserved, so `"True"` becomes `"False"` rather than `false`:

| before | after |
|---|---|
| `true` | `false` |
| `TRUE` | `FALSE` |
| `True` | `False` |
| `"true"` | `"false"` |
| `'false'` | `'true'` |

Applies to bool literals inside actions, hardcoded YAML booleans on action-free template lines, and
boolean scalars in `values.yaml`. Mapping *keys* are never flipped — a key literally named `true`
must not be renamed into a different field.

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

And a bool literal inside an action, `templates/deployment.yaml:30`:

```diff
-        runAsNonRoot: {{ .Values.securityContext.runAsNonRoot | default true }}
+        runAsNonRoot: {{ .Values.securityContext.runAsNonRoot | default false }}
```

**Why it matters.** Flipping a `values.yaml` default is the cheapest way to find out whether any test
pins that default. A chart shipping `serviceAccount.create: true` whose suite never asserts the
ServiceAccount exists will not notice the flip.

**The weak test it exposes.** A suite that never exercises a feature toggle's *shipped* value. All 14
of the weak suite's scored `bool-flip` mutants survive. The strong suite kills all 15, and how it does
so is instructive: 10 by `equal`, but the rest need assertions that check absence —
`podDisruptionBudget.enabled: true → false` is caught by `hasDocuments`, and both
`ingress.tls.enabled` and `config.debug` are caught by `notExists`. Without those, a toggle flipped
*off* produces a smaller manifest that an `equal`-only suite never looks at.

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

Only the *head* of a command is swapped — an operator name appearing as a later argument is a value,
not a call, and `{{ .Values.eq }}` is a field named `eq`. `not` is left alone; negating a negation is
[`cond-negate`](#cond-negate)'s job.

**Before / after.** A numeric threshold in `templates/configmap.yaml:16`:

```diff
-  cache-enabled: {{ ge (int .Values.config.cacheSizeMb) 64 | quote }}
+  cache-enabled: {{ lt (int .Values.config.cacheSizeMb) 64 | quote }}
```

A compound guard in `templates/poddisruptionbudget.yaml:1`, which yields two candidates — one for
`and`, one for `gt`:

```diff
- {{- if and .Values.podDisruptionBudget.enabled (gt (int .Values.replicaCount) 1) }}
+ {{- if or  .Values.podDisruptionBudget.enabled (gt (int .Values.replicaCount) 1) }}
```

```diff
- {{- if and .Values.podDisruptionBudget.enabled (gt (int .Values.replicaCount) 1) }}
+ {{- if and .Values.podDisruptionBudget.enabled (le (int .Values.replicaCount) 1) }}
```

Only the operator is replaced; the operands are untouched.

**Why it matters.** `eq`/`ne` and the ordering pairs are true inversions. `and`/`or` is not an
inversion, but it changes *which* inputs produce output, which is what exposes a suite that only ever
exercises one combination of flags — the common case for a chart with several interacting toggles.

**The weak test it exposes.** A suite that tests one side of a branch and not the other. The weak
suite kills none of its 3 scored swaps. The strong suite kills all 13, and again the assertion mix is
the point: `hasDocuments` catches both swaps in the PodDisruptionBudget guard (flipping either one
makes the resource appear or vanish), `notExists` catches the `and → or` in
`templates/configmap.yaml:26` and `templates/ingress.yaml:14`, and `lengthEqual` catches the two `eq`
swaps in the deployment's `env` branch.

The three `ternary`-driven swaps in `configmap.yaml` (`ge`, `lt`, `le` at lines 16, 18 and 20) are all
caught by plain `equal`, because each one flips a rendered ConfigMap value outright.

---

## `cond-negate`

**Negate `if`, `else if` and `with` conditions: `COND` → `not (COND)`.**

The condition is parenthesised because it may be a multi-argument call, where `not and .a .b` would
not parse. `range` is not negated (there is no boolean to invert). An `else if` yields its own
candidate, so a chain is fully covered.

**Before / after.** A whole-resource guard, `templates/hpa.yaml:1`:

```diff
- {{- if .Values.autoscaling.enabled }}
+ {{- if not (.Values.autoscaling.enabled) }}
```

A compound condition, parenthesised so it still parses — `templates/_helpers.tpl:21`:

```diff
- {{- if and .Values.serviceAccount.create .Values.serviceAccount.name -}}
+ {{- if not (and .Values.serviceAccount.create .Values.serviceAccount.name) -}}
```

An already-negated condition is negated again rather than simplified,
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

Trim markers survive exactly, and conditions inside `define` blocks are found — `_helpers.tpl` is
largely define blocks, and missing them would mean missing all of a chart's helper logic. The fixture
chart's `else if` chain at `templates/deployment.yaml:79` and `:82` produces one candidate each.

**Why it matters.** This is the highest-value mutator for Helm charts. A chart guards whole resources
behind `{{- if .Values.ingress.enabled }}`, and a suite that only tests the enabled case never notices
the resource appearing when it should not.

**The weak test it exposes.** A missing negative case. The weak suite kills none of its 10 scored
negations. The strong suite kills all 19, and **10 of those 19 need an assertion other than `equal`**:
`hasDocuments` ×4 for the four whole-resource guards (`hpa.yaml:1`, `ingress.yaml:1`,
`serviceaccount.yaml:1`, `poddisruptionbudget.yaml:1`), `lengthEqual` ×5 for the conditional list
entries, and `notExists` ×1 for the ingress TLS block.

That ratio is the practical lesson: for `cond-negate`, "this resource should not exist" is
unassertable without `hasDocuments`, `notExists` or `lengthEqual`, no matter how many `equal`
assertions you add.

---

## `default-drop`

**Remove a `| default X` segment from a pipeline.**

The rest of the pipeline is preserved, including a `default` in the middle of a longer chain:

| before | after |
|---|---|
| `{{ .Values.image.tag \| default .Chart.AppVersion }}` | `{{ .Values.image.tag }}` |
| `{{ .Values.replicaCount \| default 3 }}` | `{{ .Values.replicaCount }}` |
| `{{ .Values.tag \| default "latest" \| quote }}` | `{{ .Values.tag \| quote }}` |

Not mutated: pipelines without a `default` segment, a *field* named `default`
(`{{ .Values.default }}`), and `default` in leading position (`{{ default .Values.a }}`) where there
is nothing to pipe from.

**Before / after.** All seven of the fixture chart's `default-drop` mutants, which between them show
every shape:

| site | mutation |
|---|---|
| `_helpers.tpl:2` | `{{- $name := .Values.nameOverride \| default .Chart.Name -}}` → `{{- $name := .Values.nameOverride -}}` |
| `configmap.yaml:8` | `{{ .Values.config.logLevel \| default "info" \| quote }}` → `{{ .Values.config.logLevel \| quote }}` |
| `configmap.yaml:13` | `{{ .Values.config.traceSampleRatio \| default 0.1 \| quote }}` → `{{ .Values.config.traceSampleRatio \| quote }}` |
| `deployment.yaml:25` | `{{ .Values.terminationGracePeriodSeconds \| default 30 }}` → `{{ .Values.terminationGracePeriodSeconds }}` |
| `deployment.yaml:30` | `{{ .Values.securityContext.runAsNonRoot \| default true }}` → `{{ .Values.securityContext.runAsNonRoot }}` |
| `deployment.yaml:33` | `{{ .Values.image.tag \| default .Chart.AppVersion }}` → `{{ .Values.image.tag }}` |
| `serviceaccount.yaml:8` | `{{ .Values.serviceAccount.automount \| default true }}` → `{{ .Values.serviceAccount.automount }}` |

Note the two middle-of-pipeline cases: dropping `| default "info"` keeps `| quote` intact.

**Why it matters.** `{{ .Values.image.tag | default .Chart.AppVersion }}` has two behaviours: the
value path and the fallback path. Suites overwhelmingly test only the first, leaving the fallback —
the part that decides what a *fresh install* renders — entirely unasserted. Dropping the default makes
the field render empty.

**The weak test it exposes.** A suite that only overrides the value and never tests the fallback. The
fixture chart is built to make this concrete: `image.tag`, `terminationGracePeriodSeconds`,
`serviceAccount.automount`, `securityContext.runAsNonRoot` and `config.logLevel` are all *deliberately
left undeclared or empty in `values.yaml`*, with comments saying so, precisely so the `| default`
branch is the branch that actually renders.

The weak suite kills none of its 4 scored drops. The strong suite kills all 7, every one by `equal` —
this is the one mutator where plain value assertions are sufficient, because dropping a default always
changes a rendered value.

---

## `num-literal`

**Perturb numeric literals, two variants per site: `n` → `n+1`, and `n` → `0`.**

The two variants get distinct mutant IDs (`…:num-literal:increment`, `…:num-literal:zero`).
Deduplicated so neither is ever a no-op: for `0` the zero variant would change nothing, so the
increment (`1`) is kept and the duplicate dropped.

Quoting, integer/float form and the original number of decimal places are all preserved:

| original | increment | zero |
|---|---|---|
| `3` | `4` | `0` |
| `-2` | `-1` | `0` |
| `0` | `1` | *(dropped as a duplicate)* |
| `1.5` | `2.5` | `0.0` |
| `0.25` | `1.25` | `0.00` |

Applies to numbers inside actions, hardcoded YAML numbers on action-free template lines, and numeric
scalars in `values.yaml`.

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

A float in `values.yaml:82`, keeping the decimal places:

```diff
-  backoffFactor: 1.5
+  backoffFactor: 2.5
```

**Layout arguments are deliberately not exempt.** `nindent 4`, `indent 8`, `trunc 63`, `substr 0 5`
are all mutated. A guard existed here originally, on the assumption that reindenting always breaks
rendering and yields a useless `Invalid` mutant. Measurement disproved the "always": pinned by
`TestNumLiteralMutatesLayoutArguments`.

The fixture chart shows exactly what happens, across all 14 of its `nindent`/`toYaml | nindent` sites:

- **Every `+1` variant survives** — all 14 of them, and **all 14 are
  [equivalent mutants](concepts.md#equivalent-mutants)**. YAML does not care about a block mapping's
  absolute indentation, only that it is deeper than its parent, so `nindent 4 → 5` emits different
  bytes and identical parsed data. No assertion can catch them, and chasing them is wasted effort.
- **Every `→ 0` variant either kills or breaks rendering** — 9 `Killed`, 5 `Invalid`. Collapsing the
  indentation to zero merges the block into its parent, which either changes a value a test pins, or
  produces YAML that no longer parses (`yaml: line 22: did not find expected key`).

So the increment variant on an `indent`/`nindent` argument is not a useful mutant, while the `→ 0`
variant at the same site is. Verified by rendering both and comparing parsed documents: the tool's
status matched that independent comparison at all 14 sites.

`num-literal` is one of only two mutators that leave anything unkilled in the strong run, and every one
of those 14 is that unkillable increment — reported as `Equivalent`, not `Survived`, by the tool's
automatic check. Where the `→ 0` mutation does break rendering, the `Invalid` classification handles it
honestly instead of silently.

**Why it matters.** The increment variant catches off-by-one blindness; the zero variant catches
fields nobody asserts at all.

**The weak test it exposes.** `exists: spec.ports` cannot tell port 80 from port 81. Any assertion
that checks presence rather than value is blind to this entire mutator — which is why the weak suite
scores 0.0% on all 55 of its scored `num-literal` mutants. The strong suite kills 68 of 82, 65 of them
by plain `equal`.

---

## `range-empty`

**Force a `range` loop to iterate zero times: `range X` → `range list`.**

`list` is sprig's empty-list constructor, so the loop body never runs and everything it produced
disappears from the manifest. Variable declarations are preserved: `range $k, $v := X` becomes
`range $k, $v := list`, because dropping them leaves the body's `$k` and `$v` undeclared and the
template no longer parses — an unparseable mutant is `Invalid`, caught by every test regardless of
what it asserts, and grades nothing.

**Before / after.** A list, `testdata/charts/rangeloop/templates/configmap.yaml:7`:

```diff
-   {{- range .Values.ports }}
+   {{- range list }}
```

A map, where the declarations survive — `:10`:

```diff
-   {{- range $k, $v := .Values.labels }}
+   {{- range $k, $v := list }}
```

The whole expression is replaced, not just its first term, so a piped source collapses too:

```diff
- {{- range .Values.hosts | sortAlpha }}
+ {{- range list }}
```

Where the `range` has an `{{ else }}`, the mutant renders the **else** branch rather than nothing.
That is still a change to the manifest, so it is still a useful mutant — but "the block disappears"
is the wrong intuition for that shape.

**Why it matters.** Before this mutator, loops were completely unmutated: `cond-negate` covers `if`
and `with`, and nothing targeted `range`. Charts are full of them — env vars, ports, ingress hosts,
volume mounts, image pull secrets — so a suite could assert nothing whatsoever about any loop's
output and still score 100%. This is to `range` what `cond-negate` is to `if`.

**The weak test it exposes.** A suite that checks a resource exists but never what its loops
produced. `isKind` and a single `equal` on a static key cannot notice every port, label or host
vanishing at once. Measured on `testdata/charts/rangeloop`, whose three loops yield three mutants:
the weak suite kills **0 of 2** scored ones, the strong suite kills **2 of 2**.

**When it is correctly excluded.** A loop over a collection that is empty under every covering test
context cannot change what renders, so no assertion could catch it either. Those are reported as
[`Equivalent`](concepts.md#equivalent-mutants) and leave the score's denominator, like any other
unkillable mutant. `range` evaluates its pipeline even when the result is empty, so the execution
probe can prove the loop was reached and the verdict rests on evidence rather than on a guess. The
fixture's `extras: []` loop is exactly this case, and is reported `Equivalent` under both suites.

---

## `required-drop`

**Strip a `required "msg" X` guard down to plain `X`.**

The deletion covers `required "msg" ` and leaves the value argument in place, so the surrounding
pipeline survives:

| before | after |
|---|---|
| `{{ required "token is required" .Values.token }}` | `{{ .Values.token }}` |
| `{{ required "msg" .Values.token \| quote }}` | `{{ .Values.token \| quote }}` |

A `required` call with no value argument has nothing to keep, so it is left alone.

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

The first case shows why the pipeline is preserved: `| quote` stays.

The deletion is anchored on the *end of the message argument*, not on the value's own position. A
`FieldNode` reports a position past its first segment, so anchoring on `.Values.token` would make it
appear to start at `.token` and the deletion would swallow `.Values`. See
[the gotchas in `CLAUDE.md`](../CLAUDE.md#gotchas-that-have-already-cost-time).

**Why it matters.** `required` is the chart author's contract that a value must be supplied. The only
way to verify it is a negative test — helm-unittest's `failedTemplate` assertion — and negative tests
are exactly what suites tend to lack. If removing the guard changes nothing, nobody is testing the
failure path.

**The weak test it exposes.** A missing `failedTemplate` assertion. This is the one mutator in the set
that **only** a `failedTemplate` can kill: all three of the fixture chart's `required-drop` mutants are
killed by `failedTemplate` and by nothing else. The weak suite kills neither of its 2 scored mutants.

If your chart uses `required` and this mutant survives, you have an unverified contract.

Note that [`str-literal`](#str-literal) mutating the `required` *message* is a separate, also-useful
mutant — the message is asserted by `failedTemplate`'s `errorMessage`, so a suite that checks the
message kills it and a suite that does not, does not. Three of the fixture's `str-literal` kills come
from exactly that.

---

## `str-literal`

**Replace a string literal with the sentinel `helm-mutation-test`.**

The sentinel is deliberately recognisable, so if it ever leaks into a rendered manifest during
debugging its origin is obvious. Quote style is preserved, defaulting to double quotes so the
sentinel is always an unambiguous YAML string.

Applies to string literals inside actions, hardcoded string values on action-free template lines, and
string scalars in `values.yaml`. A literal that already equals the sentinel is skipped.

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

- **`include` / `template` / `tpl` target strings.** Replacing one makes the call look up a template
  that does not exist, which is a render error. Measured against the fixture chart, it is killed by
  every suite alike, so it grades nothing and only adds `Invalid` noise. This is the only exclusion
  in the whole mutator set that evidence supports.
- **Format strings** (anything containing a `%` verb). Swapping in a sentinel with no verbs leaves the
  arguments unconsumed, so Go emits `%!(EXTRA …)` into the manifest — a confusing artifact rather
  than a clean mutation. This is a scoping choice, not a rendering problem.

A `required` message is explicitly **not** excluded. Mutating it survives a weak suite and is caught
by a suite that asserts the message via `failedTemplate`, which is a real finding.

**Why it matters.** This is the mutator that punishes presence-only assertions. A suite asserting that
`imagePullPolicy` *exists*, without asserting what it *equals*, cannot tell `IfNotPresent` from a
sentinel.

**The weak test it exposes.** `exists:` where `equal:` was needed. The weak suite kills only **2 of 54**
scored `str-literal` mutants, and both of those it kills by accident — they are the two that hit
`kind:` (`kind: Deployment` and `kind: Service`), which its `isKind` assertions happen to cover.
Everything with actual content in it survives.

The strong suite kills **all 83**, with the widest assertion spread of any mutator: `equal` ×63,
`isAPIVersion` ×7, `isKind` ×7, `failedTemplate` ×3 (the `required` messages), `lengthEqual` ×2 and
`notExists` ×1. This is the mutator that most rewards a suite for asserting `apiVersion` and `kind`
explicitly rather than assuming them.

---

## `yaml-key-delete`

**Delete a key together with its nested block.**

The bluntest and often most revealing mutator, and by volume the largest: 231 of the fixture chart's
458 mutants.

For `values.yaml` it uses the YAML node tree, which knows the real structure. For templates it works
line by line, because a template is not valid YAML before rendering and so has no node tree to
consult.

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

Reports show only the first deleted line plus a count, since a block can be long — the fixture's
`values.yaml` `ingress:` key deletion removes 10 lines, and its `spec:` deletions in
`templates/deployment.yaml` remove dozens.

**Two safety rules, both about template syntax rather than semantics:**

- A key line containing an action is skipped only when the action is *control flow*
  (`if`/`else`/`end`/`range`/`with`/`define`/`block`). Deleting half of an `if`/`end` pair leaves an
  unbalanced template, which is a syntax error rather than a mutation.
- A key whose block contains unbalanced control flow is skipped for the same reason. A block is
  eligible when its openers and `{{ end }}`s balance and it contains no `{{ else }}`.

**No key is exempt from deletion.** `apiVersion`, `kind` and `metadata` were originally skipped, on
the assumption that Helm rejects a manifest without them. Measurement disproved that: all three render
fine when removed, survive the weak suite and are caught by the strong one — by `isAPIVersion` and
`isKind`, ×7 each, which is precisely what those assertions are for. Deleting `kind` is caught even by
the *weak* suite: the tool working as intended, not noise.

**Why it matters.** If a whole field can vanish from a rendered manifest with every test still
passing, that field is completely unasserted. There is no more direct statement of the problem.

**The weak test it exposes.** Everything that `exists` and `isKind` do not reach. The weak suite kills
**4 of 139** scored deletions, and its four kills are exactly the reach of its four assertions: two
`kind:` lines caught by `isKind`, and the Service's `spec:` and `ports:` blocks caught by `exists`.
Everything with content in it survives — `replicas`, `imagePullPolicy`, `containerPort`, both probes,
`resources`, the whole `env:` block, every label.

**The 7 mutants left unkilled in the strong run are all `values.yaml` keys**, reported as `Equivalent`
by the tool rather than `Survived`, and every one is a case where deleting the key changes nothing
that renders:

| survivor | why deleting it renders identically |
|---|---|
| `values.yaml:6` `tag: ""` | the template's `\| default .Chart.AppVersion` produces the same output either way |
| `values.yaml:14` `name: ""` | empty and absent both fall through `sample.serviceAccountName` to the same branch |
| `values.yaml:44` `enabled: false` (autoscaling) | absent is falsey, so the HPA stays absent |
| `values.yaml:49` `targetMemoryUtilizationPercentage: 0` | `0` is falsey, so the memory metric was already omitted |
| `values.yaml:66` `enabled: false` (ingress) | absent is falsey |
| `values.yaml:73` `enabled: false` (ingress TLS) | absent is falsey |
| `values.yaml:83` `debug: false` | absent is falsey |

The pattern generalises: **deleting a `values.yaml` key whose value is already its type's zero value
is almost always an [equivalent mutant](concepts.md#equivalent-mutants).** Removing the key leaves the
value nil, and Go templates treat nil and the zero value identically both for truthiness and for
`| default`. All seven above are `""`, `false` or `0`.

These seven are not merely parsed-equal — they render **byte-identical** manifests, verified under both
the chart's default values and with every feature toggle enabled. No assertion can catch them, so the
tool's equivalence check reclassifies all seven, and `yaml-key-delete` scores a genuine 100% against
the strong suite rather than the 96.8% a naive killed/survived count would otherwise show.

The equivalence also survives value overrides: a suite that sets the key supplies its own value whether
or not `values.yaml` declares it, so the deletion is invisible under every value set.

**This mutator produces 15 of the strong run's 20 `Invalid` mutants**, all from `values.yaml`.
Deleting a whole values block leaves the template dereferencing nil:

```
values.yaml:3   image:                 → nil pointer evaluating interface {}.repository
values.yaml:12  serviceAccount:        → nil pointer evaluating interface {}.create
values.yaml:22  securityContext:       → nil pointer evaluating interface {}.runAsUser
values.yaml:35  probes:                → nil pointer evaluating interface {}.enabled
values.yaml:76  config:                → nil pointer evaluating interface {}.logLevel
values.yaml:88  apiToken: …            → execution error: apiToken is required
values.yaml:89  adminEmail: …          → execution error: adminEmail is required
```

*(7 of 15 shown.)* Those are correctly excluded from the score: every test "catches" them regardless
of what it asserts. Twenty invalid out of 458 keeps the rate at 4.4%, well under the 20% ceiling that
`TestInvalidMutantRateIsLow` enforces.

If this mutator dominates your report and you want a quicker signal, `--exclude-mutators
yaml-key-delete` leaves the seven more surgical ones.

---

## Adding a mutator, or a guard

Guards need evidence, not intuition. Four were removed from this codebase because measurement
disproved their premise. Before adding one, verify the mutation actually breaks rendering:

```console
# copy the fixture, apply the mutation by hand, then:
helm unittest -f 'tests/weak_test.yaml'   /tmp/probe   # should survive
helm unittest -f 'tests/strong_test.yaml' /tmp/probe   # should be killed
```

If it survives one and is killed by the other, it is a useful mutant — do not guard it. Note that
"sometimes breaks rendering" is not grounds for a guard either: `num-literal` on `nindent` produces 14
survivors, 9 kills and 5 invalids on the fixture chart, and the useful signal in the first two
outweighs the noise in the third. Watch the invalid rate as you go; `TestInvalidMutantRateIsLow` fails
above 20%.

Shared invariants live in loops over `IDs()` in `internal/mutator/mutator_test.go`, so a new mutator
inherits them automatically: no no-op candidates, no inverted spans, no panics on unparseable
templates, and mutants that still parse.
