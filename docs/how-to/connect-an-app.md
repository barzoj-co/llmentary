# Connect an existing app

The getting-started pages create the OpenAI client inside the test. In a real
app the client is usually created by application code, often at import time or
on startup. This page shows how to route those calls through llmentary and
attribute them to the right test.

Pick the first case that fits:

| Your situation | Use |
|---|---|
| The test can pass an HTTP client, headers or a base URL into the code that creates the SDK client | [Session header](#session-header) |
| The app reads the base URL from configuration such as `OPENAI_BASE_URL` | [Session in the base URL](#session-in-the-base-url) |
| The app runs in another process or container that the test does not start | [Separate process](#separate-process) |

## Session header

Pass the session's headers (or, in Go, its HTTP client) to the place where your
app builds the SDK client:

=== "Python"

    ```python
    def test_reply(llmentary):
        bot = SupportBot(
            openai=OpenAI(base_url=llmentary.openai_base_url, default_headers=llmentary.headers),
        )
        bot.reply("Where is my order?")
        llmentary.expect().tool_called("get_order")
    ```

=== "TypeScript"

    ```ts
    test("reply", async () => {
      const lm = llmentary();
      const bot = new SupportBot(new OpenAI(lm.openAI()));
      await bot.reply("Where is my order?");
      await lm.expect().toolCalled("get_order");
    });
    ```

=== "Go"

    ```go
    func TestReply(t *testing.T) {
        lm := llmentary.New(t)
        bot := NewSupportBot(openai.NewClient(
            option.WithBaseURL(lm.OpenAIBaseURL("")),
            option.WithHTTPClient(lm.HTTPClient()),
        ))
        bot.Reply(context.Background(), "Where is my order?")
        lm.Expect(context.Background()).ToolCalled("get_order")
    }
    ```

## Session in the base URL

When the app only lets you configure a base URL, put the session id in the
path: `http://127.0.0.1:7777/s/<session-id>/v1`. The proxy strips the
`/s/<session-id>` prefix before forwarding. Each client has a helper that
builds this URL.

Set it **before** the app creates its client. Most SDKs read
`OPENAI_BASE_URL` when the client is constructed, so a module-level client
must be imported after the variable is set.

=== "Python"

    ```python
    @pytest.fixture
    def app(llmentary, monkeypatch):
        monkeypatch.setenv("OPENAI_BASE_URL", llmentary.path_base_url())
        from myapp import create_app  # import after setting the variable
        return TestClient(create_app())


    def test_chat(app, llmentary):
        app.post("/chat", json={"text": "Where is my order?"})
        llmentary.expect().tool_called("get_order")
    ```

=== "TypeScript"

    ```ts
    test("chat", async () => {
      const lm = llmentary();
      process.env.OPENAI_BASE_URL = lm.pathBaseURL();
      const { app } = await import("./app.js"); // import after setting the variable
      await app.handle("Where is my order?");
      await lm.expect().toolCalled("get_order");
    });
    ```

=== "Go"

    ```go
    func TestChat(t *testing.T) {
        lm := llmentary.New(t)
        t.Setenv("OPENAI_BASE_URL", lm.PathBaseURL(""))
        app := startApp(t) // calls openai.NewClient after Setenv
        app.Handle(context.Background(), "Where is my order?")
        lm.Expect(context.Background()).ToolCalled("get_order")
    }
    ```

    Package `init` functions run before any test. If your app creates its
    client in `init`, set `OPENAI_BASE_URL` before running `go test` instead.

Other SDKs and frameworks read different variables (`OPENAI_API_BASE` for some
LangChain versions, a constructor argument for others). The table in
[SDK base-URL variables](../black-box.md#sdk-base-url-environment-variables)
lists the ones we know.

## Separate process

When the app runs in its own container or process, run the proxy next to it and
point the app's base URL at the proxy. Two ways to attribute calls:

- If each test can give the app a different base URL, use the
  `/s/<session-id>/v1` form above.
- If the app is configured once, start the proxy with
  `--default-session=<id>`. Every call without a session marker is recorded on
  that session. In Go, read it with `llmentary.New(t, llmentary.WithSessionID("<id>"))`;
  in TypeScript, with `llmentary({ sessionId: "<id>" })`. Any language can use
  the control API directly (`GET /sessions/<id>/wait`).

The control API then has to be reachable from the test, which usually means
binding it to a non-loopback address. The proxy requires `--control-token` in
that case.

A complete docker compose example, and how to check that two sessions did not
see each other's messages, are in [Black-box and sidecar setups](../black-box.md).
