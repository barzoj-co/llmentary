# Assert on model output

What your app sends to the model is under your control. What the model writes
back is not: the same prompt produces different wording on every run. This page
shows how to test the answer without making the test flaky.

## Check the input to the answer first

Most "the answer is wrong" bugs are input bugs: the tool result was not sent
back, the wrong tool ran, the system prompt was missing a rule. Those are
recorded exactly and can be asserted exactly.

Put a distinctive marker in your tool fixtures and check that it reached the
model:

```python
ORDER = {"id": "A-1001", "status": "shipped", "note": "SEED_ORDER_STATUS"}

def test_answer_uses_order_status(llmentary, bot, fake_orders):
    fake_orders.returns(ORDER)
    bot.reply("Where is my order?")

    check = llmentary.expect()
    check.tool_called("get_order")
    check.tool_result_in_prompt("get_order")   # the next request carried the result
    check.prompt_contains("SEED_ORDER_STATUS") # including this exact value
```

`tool_result_in_prompt` checks that the request after the tool call contains
that call's result. `tool_result_fed_back` is weaker: it only checks that some
later request has a tool message with the matching call id.

## Then check the answer loosely

Do not expect the model to repeat a marker it was never asked to repeat. Check
for values it will naturally use, and accept several forms:

```python
check.final_response_mentions_any("shipped", "on its way")
```

`final_response_mentions_any` looks at the text of the last recorded response.
It ignores case and, by default, normalizes numbers, so `20°C`, `20 °C` and
`20 degrees` all match `20`. Numbers match as whole numbers: `20` does not
match `2025`.

When the answer reaches the test through your app's API rather than the
recording, use the plain function on that text:

=== "Python"

    ```python
    from llmentary import answer_mentions_any

    answer_mentions_any(response.json()["text"], "shipped", "on its way")
    ```

=== "TypeScript"

    ```ts
    import { mentionsAny } from "llmentary";

    expect(mentionsAny(body.text, ["shipped", "on its way"])).toBe(true);
    ```

=== "Go"

    ```go
    if !llmentary.MentionsAny(body.Text, "shipped", "on its way") {
        t.Errorf("answer does not mention the order status: %q", body.Text)
    }
    ```

## Structured output

When the model returns JSON, check that it parses and matches your schema
instead of comparing strings: `ResponseJSONValid` and `ResponseJSONSchemaValid`
in Go, `response_json_valid` and `response_json_schema` in Python,
`responseJsonValid` and `responseJsonSchema` in TypeScript.

## What llmentary does not do

llmentary does not judge whether an answer is correct, helpful or equivalent
to a reference answer. If you need that, use an evaluation tool that scores
text, and use llmentary to check that the inputs it scored were the ones you
expected.
