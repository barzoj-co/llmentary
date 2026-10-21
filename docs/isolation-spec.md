# Isolation spec (`isolated_from`)

This is the single cross-language definition of
`IsolatedFrom` / `isolated_from` / `isolatedFrom`. Client READMEs link here
instead of restating the rules. Normative fixtures live in
[`testdata/isolation/`](../testdata/isolation/README.md). Every client vendors
those JSON files and must produce the same verdicts.

Related: [Black-box: proving isolation](black-box.md#proving-isolation),
[DOG-20](black-box.md#what-a-path-prefix-isolates), [DOG-22](isolation-spec.md#rule-3-no-foreign-user-turn).

## Verdict

Session **X** is isolated from session **Y** when all four rules hold. The
helper is directional: check both `X.isolated_from(Y)` and
`Y.isolated_from(X)` when the test is "two parallel runs".

```mermaid
flowchart TD
  A["X.isolated_from(Y)"] --> B{"X has calls?"}
  B -->|no and not allow_empty| E1["fail: no calls recorded for session X"]
  B -->|no and allow_empty| P["pass"]
  B -->|yes| C{"every X call.session_id == X?"}
  C -->|no| E2["fail: foreign session_id"]
  C -->|yes| D{"any call id in both X and Y?"}
  D -->|yes| E3["fail: shared call id"]
  D -->|no| F{"foreign introduced user turn in an X request?"}
  F -->|yes| E4["fail: user text from Y appears in X"]
  F -->|no| P
```

## Options

| Option | Default | Meaning |
|---|---|---|
| `allow_empty` | `false` | Empty X is a failure unless this is set (DOG-21). |
| `min_chars` | `8` | Normalized texts shorter than this are ignored for rule 3. |
| `ignore_texts` | `[]` | Extra texts ignored for rule 3 after normalization. |

System / developer prompts and tool definitions are never compared.

## Rule 1 — session id

Every call in X has `session_id == X`'s id.

Failure fragment: `session_id`.

## Rule 2 — call ids are disjoint

No `call.id` appears in both sessions.

Failure fragment: `call id`.

## Rule 3 — no foreign user turn

Comparison is an **exact match after normalization**, never a substring.

Normalization: trim, collapse any whitespace to a single space, Unicode
casefold. Multi-part `content` / `input` items are joined in order (the `text`
or `input_text` / `output_text` field of each part).

User-role messages only:

- Chat Completions: `messages[role=user]`
- Responses: `input` as a string (one user turn) or `input[role=user]` items

**Introduced** texts of a session: walk its calls in `seq` order.

- The **first** call treats only its **last** user-role message as introduced
  (the new turn). Earlier user messages on that first request are inbound
  history, not introductions.
- Later calls introduce each user-role message whose normalized form was not
  already present in an earlier call of the same session.

A leak is a user-role message **appearing in any request of X** (history
included) whose normalized form is in `introduced(Y)` and not in
`introduced(X)`, after `min_chars` / `ignore_texts`.

Texts both sessions introduced themselves are **not** a leak. That is why two
independent runs of the same canned question pass.

Failure shape (required fragments):

```text
user text '<text>' from session <Y> #<seq> (<call_id>) appears in session <X> #<seq> (<call_id>)
```

`<text>` is the leaked user string as stored (trimmed, original case). Both
`#seq (call_id)` pairs are present: Y is the session that introduced the
text, X is the session whose request contains it.

## Rule 4 — empty X

If X has no calls and `allow_empty` is false, fail with
`no calls recorded for session <X>`. If `allow_empty` is true, pass.

## What is not a leak

- Identical system / developer prompts
- Shared tool names or schemas
- The same canned user question sent independently by both sessions
- A short text such as `Hi` that is only a substring of the other session's
  user turn (`min_chars` also drops texts shorter than 8 by default)

## Parallel same-scenario example

```python
lm_a = llmentary()  # or Go llmentary.New(t) / JS llmentary()
lm_b = llmentary()
# both apps send "What is my biological age?"
lm_a.expect().isolated_from(lm_b)  # pass
lm_b.expect().isolated_from(lm_a)  # pass
```

A third session that **replays** A's first user message as history (Mongo /
checkpointer / conversation state) fails rule 3 with the spec message above.
