# Test one turn of a conversation

A multi-turn test records every call in the session. To assert on the second
turn only, take a mark before it and scope the assertions to calls after the
mark.

In these examples `bot` is your application's chat client, created with the
llmentary base URL and session headers as shown in
[Connect an existing app](connect-an-app.md).

=== "Python"

    ```python
    def test_follow_up(llmentary, bot):
        bot.reply("Where is my order?")
        mark = llmentary.mark()

        bot.reply("Can I change the delivery address?")

        turn = llmentary.expect(since=mark)
        turn.call_count(1).tool_called("update_address").not_called("cancel_order")
    ```

=== "TypeScript"

    ```ts
    test("follow-up", async () => {
      const lm = llmentary();
      await bot.reply("Where is my order?");
      const mark = await lm.mark();

      await bot.reply("Can I change the delivery address?");

      const turn = lm.expect({ since: mark });
      await turn.callCount(1);
      await turn.toolCalled("update_address");
      await turn.notCalled("cancel_order");
    });
    ```

=== "Go"

    ```go
    func TestFollowUp(t *testing.T) {
        ctx := context.Background()
        lm := llmentary.New(t)
        bot.Reply(ctx, "Where is my order?")
        mark := lm.Mark(ctx)

        bot.Reply(ctx, "Can I change the delivery address?")

        turn := lm.Expect(ctx, llmentary.Since(mark))
        turn.CallCount(1)
        turn.ToolCalled("update_address")
        turn.ToolNotCalled("cancel_order")
    }
    ```

`mark()` returns the sequence number of the last recorded call. Calls are
numbered from 1 within a session. In TypeScript, `mark()` waits for the session
to be quiet first. In Python and Go it reads the session immediately, so take
the mark only after the previous turn has finished (for example, after
asserting on it); otherwise a late call from the first turn is counted in the
second.

## Filter by what the call offered

Agents often make several kinds of calls in one turn: a router call with no
tools, then a specialist call with a set of tools. Filter by the tools a call
offered to assert on each kind separately:

=== "Python"

    ```python
    llmentary.query().without_tools().expect().call_count(1)
    llmentary.query().offers_tool("get_order").expect().tool_called("get_order")
    ```

=== "TypeScript"

    ```ts
    await lm.expect({ withoutTools: true }).callCount(1);
    await lm.expect({ offersTool: "get_order" }).toolCalled("get_order");
    ```

=== "Go"

    ```go
    trace := lm.Calls(ctx)
    trace.Query().WithoutTools().Expect(t).CallCount(1)
    trace.Query().OffersTool("get_order").Expect(t).ToolCalled("get_order")
    ```

Other filters select calls by model, API type, HTTP status, outcome, errors,
retries or position. They are listed in the
[assertions reference](../reference/assertions.md#select-calls).

## Empty selections fail

If a mark or filter selects no calls, `NoErrors` and `ToolNotCalled` fail
instead of passing on nothing:

```text
no calls matched query (without_tools, since=3); session has 8 calls
```

This catches a turn that never reached the model. When zero calls is the
expected result, assert it directly with `CallCount(0)`, or opt out with
`AllowEmpty()` / `allow_empty=True` / `allowEmpty()`.
